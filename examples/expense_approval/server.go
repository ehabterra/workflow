package main

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ehabterra/workflow"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server is the thin web layer over App: server-rendered pages plus the
// webhook-style endpoints an external system would call.
type Server struct {
	app  *App
	tmpl *template.Template
	mux  *http.ServeMux
}

// NewServer builds the routes. Pages: dashboard (filterable), submit form,
// expense detail (lanes, payment status, revise), batch page (with manual
// release of guard-held payouts). Endpoints: approve/reject/revise webhooks,
// payment release, batch run, reconcile, metrics, healthz.
func NewServer(app *App) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"money": func(v float64) string { return fmt.Sprintf("%.2f", v) },
		// stateClass turns a state label into a CSS class suffix
		// ("in review" -> "in-review").
		"stateClass": func(s string) string { return strings.ReplaceAll(s, " ", "-") },
		// held mirrors the payment net's guard so the batch page can flag
		// tokens the run will hold back.
		"held": func(t workflow.Token) bool {
			v, ok := t.Get("amount")
			if !ok {
				return false
			}
			f, ok := v.(float64)
			return ok && f > payGuardLimit
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
		// deadline humanizes an absolute next-due instant relative to now.
		"deadline": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			d := time.Until(*t)
			if d < 0 {
				return "overdue " + humanDur(-d)
			}
			return "in " + humanDur(d)
		},
		"overdue": func(t *time.Time) bool { return t != nil && time.Now().After(*t) },
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
	s.mux.HandleFunc("POST /expenses/{id}/revise", s.revise)
	s.mux.HandleFunc("POST /payments/{id}/release", s.releasePayment)
	s.mux.HandleFunc("GET /batch", s.batchPage)
	s.mux.HandleFunc("POST /batch/run", s.runBatch)
	s.mux.HandleFunc("POST /batch/reconcile", s.reconcile)
	s.mux.HandleFunc("GET /metrics", s.metrics)
	s.mux.HandleFunc("GET /healthz", s.healthz)
	return s, nil
}

// humanDur renders a duration in at most two human units ("2d 3h", "5m").
func humanDur(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := d / (24 * time.Hour)
		hours := (d % (24 * time.Hour)) / time.Hour
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, hours)
	case d >= time.Hour:
		h := d / time.Hour
		m := (d % time.Hour) / time.Minute
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	start := time.Now()
	s.mux.ServeHTTP(rec, r)
	if r.URL.Path != "/healthz" { // probes would drown the log
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	}
}

// statusRecorder captures the response code for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// wantsHTML reports whether the caller is a browser (form navigation) rather
// than a webhook/API client; browsers get redirects with flash feedback.
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// respond answers an action: browsers are redirected to `back` with the
// message as a flash banner; API callers get the status code and plain text.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, back string, code int, msg string) {
	if wantsHTML(r) {
		sep := "?"
		if strings.Contains(back, "?") {
			sep = "&"
		}
		http.Redirect(w, r, back+sep+"flash="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	w.WriteHeader(code)
	fmt.Fprintln(w, msg)
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
	// Optional state filter (the stat chips double as filters).
	filter := r.URL.Query().Get("state")
	if filter != "" {
		kept := views[:0]
		for _, v := range views {
			if v.State == filter {
				kept = append(kept, v)
			}
		}
		views = kept
	}
	s.render(w, "dashboard.html", map[string]any{
		"Flash":    r.URL.Query().Get("flash"),
		"Expenses": views,
		"Payment":  payment,
		"Stats":    stats,
		"Filter":   filter,
		"Refresh":  r.URL.Query().Get("refresh") != "",
	})
}

func (s *Server) newExpenseForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "new.html", map[string]any{"Flash": r.URL.Query().Get("flash")})
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
	s.respond(w, r, "/expenses/"+id, http.StatusOK, "Expense "+id+" submitted")
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
	payStatus, err := s.app.PaymentStatus(r.Context(), view.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "detail.html", map[string]any{
		"Flash":     r.URL.Query().Get("flash"),
		"V":         view,
		"PayStatus": payStatus,
	})
}

// decision is the webhook endpoint for approve/reject. Its status codes are
// the point of the M3.4 error split:
//
//	200 fired             — the decision advanced the workflow
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
		back := "/expenses/" + id
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
			s.respond(w, r, back, http.StatusOK, "Fired "+strings.Join(fired, ", ")+" as "+actor)
		case errors.Is(err, workflow.ErrWorkflowNotFound):
			http.NotFound(w, r)
		case errors.Is(err, ErrTerminal):
			s.respond(w, r, back, http.StatusConflict, "Expense already rejected — revise it to resubmit")
		case errors.Is(err, workflow.ErrGuardRejected):
			s.respond(w, r, back, http.StatusForbidden, "A guard rejected the transition")
		case errors.Is(err, workflow.ErrNotEnabled):
			// Redelivered webhook: the branch already moved on. Idempotent.
			s.respond(w, r, back, http.StatusOK, "Already processed (no-op)")
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// revise resubmits a rejected expense with an updated amount/description —
// the loop edge of the net (rejected -> draft -> submit routing).
func (s *Server) revise(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := r.FormValue("actor")
	if actor == "" {
		actor = "submitter"
	}
	amount, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("amount")), 64)
	if err != nil {
		http.Error(w, "amount must be a number", http.StatusBadRequest)
		return
	}
	back := "/expenses/" + id
	fired, err := s.app.Revise(r.Context(), id, actor, r.FormValue("description"), amount)
	switch {
	case err == nil:
		msg := "Resubmitted for review"
		for _, n := range fired {
			if n == "submit_auto" {
				msg = "Resubmitted and auto-approved (petty cash)"
			}
		}
		s.respond(w, r, back, http.StatusOK, msg)
	case errors.Is(err, workflow.ErrWorkflowNotFound):
		http.NotFound(w, r)
	case errors.Is(err, workflow.ErrNotEnabled):
		s.respond(w, r, back, http.StatusConflict, "Only a rejected expense can be revised")
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// releasePayment is the manual-review action for a guard-held payout.
func (s *Server) releasePayment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := r.FormValue("actor")
	if actor == "" {
		actor = "payment-reviewer"
	}
	back := r.FormValue("back")
	if back == "" {
		back = "/batch"
	}
	err := s.app.ReleasePayment(r.Context(), id, actor)
	switch {
	case err == nil:
		s.respond(w, r, back, http.StatusOK, "Released payment for "+id+" as "+actor)
	case errors.Is(err, ErrNotPayable):
		s.respond(w, r, back, http.StatusConflict, "Nothing to release for "+id)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) batchPage(w http.ResponseWriter, r *http.Request) {
	payment, err := s.app.Payment(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "batch.html", map[string]any{
		"Flash": r.URL.Query().Get("flash"),
		"P":     payment,
	})
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
	msg := fmt.Sprintf("paid %d expense(s), %d held by guard", len(res.Paid), res.Held)
	s.respond(w, r, "/batch", http.StatusOK, msg)
}

func (s *Server) reconcile(w http.ResponseWriter, r *http.Request) {
	rep, err := s.app.Reconcile(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	msg := fmt.Sprintf("reconciled: %d enqueued, %d marked paid, %d stale draft(s) deleted",
		rep.Enqueued, rep.Marked, rep.DraftsDeleted)
	s.respond(w, r, "/batch", http.StatusOK, msg)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, c := range s.app.metrics.snapshot() {
		fmt.Fprintf(w, "workflow_firings_total{transition=%q} %d\n", c.Name, c.Count)
	}
}

// healthz answers liveness/readiness probes: 200 when the database responds,
// 503 otherwise.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.app.db.PingContext(r.Context()); err != nil {
		http.Error(w, "database unreachable", http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintln(w, "ok")
}
