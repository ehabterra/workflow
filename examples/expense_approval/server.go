package main

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/ehabterra/workflow"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server is the thin web layer over App: server-rendered pages plus the
// webhook-style approve/reject endpoints an external system would call.
type Server struct {
	app  *App
	tmpl *template.Template
	mux  *http.ServeMux
}

// NewServer builds the routes. Pages: dashboard, submit form, expense
// detail, batch page. Endpoints: approve/reject webhooks, batch run,
// reconcile, metrics.
func NewServer(app *App) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"money": func(v float64) string { return fmt.Sprintf("%.2f", v) },
		// stateClass turns a state label into a CSS class suffix
		// ("in review" -> "in-review").
		"stateClass": func(s string) string { return strings.ReplaceAll(s, " ", "-") },
		// held mirrors the payment net's guard (token.amount <= 5000.0) so
		// the batch page can flag tokens the run will hold back.
		"held": func(t workflow.Token) bool {
			v, ok := t.Get("amount")
			if !ok {
				return false
			}
			f, ok := v.(float64)
			return ok && f > 5000
		},
		"tokenField": func(t workflow.Token, key string) string {
			v, ok := t.Get(key)
			if !ok {
				return ""
			}
			switch n := v.(type) {
			case string:
				return n
			case float64:
				return fmt.Sprintf("%.2f", n)
			default:
				return fmt.Sprint(v)
			}
		},
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{app: app, tmpl: tmpl, mux: http.NewServeMux()}

	s.mux.HandleFunc("GET /{$}", s.dashboard)
	s.mux.HandleFunc("GET /expenses/new", s.newExpenseForm)
	s.mux.HandleFunc("POST /expenses", s.submitExpense)
	s.mux.HandleFunc("GET /expenses/{id}", s.expenseDetail)
	s.mux.HandleFunc("POST /expenses/{id}/approve", s.decision("approve"))
	s.mux.HandleFunc("POST /expenses/{id}/reject", s.decision("reject"))
	s.mux.HandleFunc("GET /batch", s.batchPage)
	s.mux.HandleFunc("POST /batch/run", s.runBatch)
	s.mux.HandleFunc("POST /batch/reconcile", s.reconcile)
	s.mux.HandleFunc("GET /metrics", s.metrics)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	views, err := s.app.ListExpenses(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	payment, err := s.app.Payment(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stats := map[string]int{}
	for _, v := range views {
		stats[v.State]++
	}
	s.render(w, "dashboard.html", map[string]any{
		"Expenses": views,
		"Payment":  payment,
		"Stats":    stats,
	})
}

func (s *Server) newExpenseForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "new.html", nil)
}

func (s *Server) submitExpense(w http.ResponseWriter, r *http.Request) {
	amount, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("amount")), 64)
	if err != nil {
		http.Error(w, "amount must be a number", http.StatusBadRequest)
		return
	}
	id, err := s.app.SubmitExpense(r.Context(), r.FormValue("submitter"), r.FormValue("description"), amount)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/expenses/"+id, http.StatusSeeOther)
}

func (s *Server) expenseDetail(w http.ResponseWriter, r *http.Request) {
	view, err := s.app.ExpenseWithHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, workflow.ErrWorkflowNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "detail.html", view)
}

// decision is the webhook endpoint for approve/reject. Its status codes are
// the point of the M3.4 error split:
//
//	200 fired            — the decision advanced the workflow
//	200 already-processed — redelivery: ErrNotEnabled, safe no-op
//	409 terminal          — the expense was already rejected
//	403 guard-rejected    — a guard refused the transition
//	404                   — no such expense
func (s *Server) decision(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		branch := r.FormValue("branch")
		actor := r.FormValue("actor")
		if actor == "" {
			actor = branch + "-reviewer"
		}
		var fired []string
		var err error
		if action == "approve" {
			fired, err = s.app.Approve(r.Context(), id, branch, actor)
		} else {
			fired, err = s.app.Reject(r.Context(), id, branch, actor)
		}
		switch {
		case err == nil:
			// An approval that completes the AND-join lands the expense in
			// approved: enqueue it for the payment batch.
			for _, name := range fired {
				if name == "finalize" {
					if err := s.app.EnqueueApproved(r.Context(), id); err != nil {
						log.Printf("enqueue %s for payment: %v (Reconcile will repair)", id, err)
					}
				}
			}
			s.respond(w, r, id, http.StatusOK, fmt.Sprintf("fired %s", strings.Join(fired, ", ")))
		case errors.Is(err, workflow.ErrWorkflowNotFound):
			http.NotFound(w, r)
		case errors.Is(err, ErrTerminal):
			s.respond(w, r, id, http.StatusConflict, "expense already rejected")
		case errors.Is(err, workflow.ErrGuardRejected):
			s.respond(w, r, id, http.StatusForbidden, "guard rejected the transition")
		case errors.Is(err, workflow.ErrNotEnabled):
			// Redelivered webhook: the branch already moved on. Idempotent.
			s.respond(w, r, id, http.StatusOK, "already processed (no-op)")
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// respond redirects browsers back to the detail page and answers API
// callers (curl, webhook senders) with a plain-text status.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, id string, code int, msg string) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/expenses/"+id, http.StatusSeeOther)
		return
	}
	w.WriteHeader(code)
	fmt.Fprintln(w, msg)
}

func (s *Server) batchPage(w http.ResponseWriter, r *http.Request) {
	payment, err := s.app.Payment(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "batch.html", payment)
}

func (s *Server) runBatch(w http.ResponseWriter, r *http.Request) {
	actor := r.FormValue("actor")
	if actor == "" {
		actor = "batch-operator"
	}
	res, err := s.app.RunBatch(r.Context(), actor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/batch", http.StatusSeeOther)
		return
	}
	fmt.Fprintf(w, "paid %d expense(s), %d held by guard\n", len(res.Paid), res.Held)
}

func (s *Server) reconcile(w http.ResponseWriter, r *http.Request) {
	enqueued, marked, err := s.app.Reconcile(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/batch", http.StatusSeeOther)
		return
	}
	fmt.Fprintf(w, "reconciled: %d enqueued, %d marked paid\n", enqueued, marked)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, c := range s.app.metrics.snapshot() {
		fmt.Fprintf(w, "workflow_firings_total{transition=%q} %d\n", c.Name, c.Count)
	}
}
