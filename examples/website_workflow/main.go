package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/yaml"
	_ "github.com/mattn/go-sqlite3"
)

// WebsiteWorkflow represents a website approval workflow
type WebsiteWorkflow struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	State       string    `json:"state"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
}

// TransitionHistory represents a workflow transition record
type TransitionHistory struct {
	ID         int64     `json:"id"`
	WorkflowID int64     `json:"workflow_id"`
	FromState  string    `json:"from_state"`
	ToState    string    `json:"to_state"`
	Transition string    `json:"transition"`
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"created_at"`
}

var (
	db            *sql.DB
	workflowDef   *workflow.Definition
	templates     *template.Template
	workflowMgr   *workflow.Manager
	workflowStore workflow.Storage
	historyStore  history.HistoryStore
	yamlConfig    *yaml.Config
	yamlLoader    *yaml.Loader
)

func init() {
	var err error

	// 1. Load YAML configuration
	configPath := "workflow.yaml"
	// Try to find config in the same directory as the executable
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Try examples/website_workflow/workflow.yaml
		exePath, _ := os.Executable()
		exeDir := filepath.Dir(exePath)
		configPath = filepath.Join(exeDir, "workflow.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			log.Fatalf("Failed to find workflow.yaml: %v", err)
		}
	}

	yamlConfig, err = yaml.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load YAML config: %v", err)
	}

	// 2. Setup storage and history from YAML config (automated)
	// This handles: storage builder registration, storage initialization, and history store setup
	var storageResult *yaml.StorageSetupResult
	if yamlConfig.Storage != nil {
		storageResult, err = yaml.SetupStorageFromConfig(yamlConfig.Storage)
		if err != nil {
			log.Fatalf("Failed to setup storage from config: %v", err)
		}
		workflowStore = storageResult.Storage
		historyStore = storageResult.HistoryStore

		// Extract SQL connection if available (for history store queries)
		if storageResult.Connection != nil {
			if sqlConn, ok := storageResult.Connection.(*yaml.SQLConnection); ok {
				db = sqlConn.DB()
			}
		}
	}

	// 5. Load workflow definition from YAML
	yamlLoader = yaml.NewLoader()
	workflowDef, err = yamlLoader.LoadDefinition(yamlConfig)
	if err != nil {
		log.Fatalf("Failed to load workflow definition: %v", err)
	}

	// 6. Create the manager with the storage
	workflowReg := workflow.NewRegistry()
	workflowMgr = workflow.NewManager(workflowReg, workflowStore)

	// Load templates with custom functions
	funcMap := template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
	}
	templates = template.Must(template.New("").Funcs(funcMap).ParseGlob("templates/*.html"))
}

func main() {
	if err := os.MkdirAll("templates", 0755); err != nil {
		log.Printf("Warning: failed to create templates directory: %v", err)
	}

	// Serve static files (CSS, JS, etc.)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	http.HandleFunc("/", handleHome)
	http.HandleFunc("/workflow/new", handleNewWorkflowForm)
	http.HandleFunc("/workflow/create", handleCreateWorkflow)
	http.HandleFunc("/workflow/", handleWorkflowPage)
	http.HandleFunc("/diagram", handleDiagram)

	log.Println("Server starting on :8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Simplified representation for the list view
type WorkflowSummary struct {
	ID    string
	Title string
	State string
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// For the home page, we query the table directly for a summary list.
	rows, err := db.Query("SELECT id, title, state FROM workflows ORDER BY id DESC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("Error closing rows: %v", err)
		}
	}()

	var summaries []WorkflowSummary
	for rows.Next() {
		var summary WorkflowSummary
		var stateJSON string
		if err := rows.Scan(&summary.ID, &summary.Title, &stateJSON); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var places []string
		if err := json.Unmarshal([]byte(stateJSON), &places); err == nil && len(places) > 0 {
			summary.State = places[0]
		} else {
			summary.State = "?"
		}
		summaries = append(summaries, summary)
	}

	if err := templates.ExecuteTemplate(w, "home.html", summaries); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func handleNewWorkflowForm(w http.ResponseWriter, r *http.Request) {
	if err := templates.ExecuteTemplate(w, "workflow-form.html", nil); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse form: %v", err), http.StatusBadRequest)
		return
	}
	title := r.FormValue("title")
	content := r.FormValue("content")

	if title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}

	id := fmt.Sprintf("content_%d", len(listWorkflowsHelper())+1)

	wf, err := workflowMgr.CreateWorkflow(context.Background(), id, workflowDef, "draft")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	wf.SetContext("title", title)
	wf.SetContext("content", content)

	if err := workflowMgr.SaveWorkflow(context.Background(), id, wf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type WorkflowPageData struct {
	ID          string
	Workflow    *workflow.Workflow
	Title       any
	Content     any
	Transitions []workflow.Transition
	History     []history.TransitionRecord
}

func handleWorkflowPage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/workflow/")
	parts := strings.Split(path, "/")
	id := parts[0]

	if r.Method == "POST" {
		action := r.FormValue("action")
		notes := r.FormValue("notes")
		actor := r.FormValue("actor") // Get actor from form (could be from session in real app)
		if actor == "" {
			actor = "user" // Default actor if not provided
		}

		wf, err := workflowMgr.GetWorkflow(context.Background(), id, workflowDef)
		if err != nil {
			http.Error(w, "Workflow not found", http.StatusNotFound)
			return
		}

		transitions, _ := wf.EnabledTransitions()
		var targetTransition *workflow.Transition
		for i := range transitions {
			if transitions[i].Name() == action {
				targetTransition = &transitions[i]
				break
			}
		}

		if targetTransition == nil {
			http.Error(w, "Transition not allowed or does not exist", http.StatusBadRequest)
			return
		}

		// Use ApplyTransitionWithHistory helper from yaml package
		// This will use YAML defaults for notes/actor if not provided, or override with runtime values
		// Use yaml.WithTemplateValue to store values with string keys for yaml helper compatibility
		ctx := context.Background()
		if notes != "" {
			ctx = yaml.WithTemplateValue(ctx, "notes", notes)
		}
		if actor != "" {
			ctx = yaml.WithTemplateValue(ctx, "actor", actor)
		}

		// Apply transition using the helper which handles history automatically
		err = yaml.ApplyTransitionByNameWithHistory(
			wf,
			targetTransition.Name(),
			historyStore,
			ctx,
			notes, // Override notes (empty string will use YAML default)
			actor, // Override actor (empty string will use YAML default or context)
			nil,   // No custom fields override
		)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to apply transition: %v", err), http.StatusInternalServerError)
			return
		}

		if err := workflowMgr.SaveWorkflow(context.Background(), id, wf); err != nil {
			http.Error(w, fmt.Sprintf("Failed to save workflow: %v", err), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
		return
	}

	wf, err := workflowMgr.GetWorkflow(context.Background(), id, workflowDef)
	if err != nil {
		http.Error(w, "Workflow not found", http.StatusNotFound)
		return
	}

	title, _ := wf.Context("title")
	content, _ := wf.Context("content")
	enabledTransitions, _ := wf.EnabledTransitions()
	history, _ := historyStore.ListHistory(context.Background(), id, history.QueryOptions{Limit: 50, Offset: 0})

	data := WorkflowPageData{
		ID:          id,
		Workflow:    wf,
		Title:       title,
		Content:     content,
		Transitions: enabledTransitions,
		History:     history,
	}

	if err := templates.ExecuteTemplate(w, "workflow.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func handleDiagram(w http.ResponseWriter, r *http.Request) {
	// The definition renders its own structure — no scratch instance needed —
	// and the ?dir= switcher picks the flow orientation.
	dir := workflow.DiagramDirection(r.URL.Query().Get("dir"))
	switch dir {
	case workflow.DiagramDirectionBottomUp, workflow.DiagramDirectionLeftRight, workflow.DiagramDirectionRightLeft:
	default:
		dir = workflow.DiagramDirectionTopDown
	}
	data := struct {
		Diagram string
		Dir     string
	}{Diagram: workflowDef.Diagram(dir), Dir: string(dir)}
	if err := templates.ExecuteTemplate(w, "diagram.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func listWorkflowsHelper() []string {
	rows, err := db.Query("SELECT id FROM workflows")
	if err != nil {
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("Error closing rows: %v", err)
		}
	}()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
