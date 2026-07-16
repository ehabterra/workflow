// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/yaml"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
	_ "github.com/mattn/go-sqlite3"
)

// Project represents a project in the workflow
type Project struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	State       []string       `json:"state"`
	Metadata    map[string]any `json:"metadata"`
	Context     map[string]any `json:"context"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// RoleConfig defines a role with its description and allowed actions
type RoleConfig struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Places      []string `json:"places"`      // States this role can see/work on
	Transitions []string `json:"transitions"` // Transitions this role can perform
}

var (
	db            *sql.DB
	workflowDef   *workflow.Definition
	templates     *template.Template
	workflowMgr   *workflow.Manager
	workflowReg   *workflow.Registry
	workflowStore workflow.Storage
	historyStore  history.HistoryStore
	yamlConfig    *yaml.Config
	store         *sessions.CookieStore
	rolesConfig   map[string]*RoleConfig
)

func init() {
	var err error

	// Load YAML configuration
	configPath := "workflow.yaml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
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

	// Setup storage and history from YAML config
	var storageResult *yaml.StorageSetupResult
	if yamlConfig.Storage != nil {
		storageResult, err = yaml.SetupStorageFromConfig(yamlConfig.Storage)
		if err != nil {
			log.Fatalf("Failed to setup storage from config: %v", err)
		}
		workflowStore = storageResult.Storage
		historyStore = storageResult.HistoryStore

		if storageResult.Connection != nil {
			if sqlConn, ok := storageResult.Connection.(*yaml.SQLConnection); ok {
				db = sqlConn.DB()
			}
		}
	}

	// Initialize session store
	store = sessions.NewCookieStore([]byte("workflow-secret-key-change-in-production"))

	// Initialize roles configuration
	initRolesConfig()

	// Load workflow definition from YAML
	yamlLoader := yaml.NewLoader()
	workflowDef, err = yamlLoader.LoadDefinition(yamlConfig)
	if err != nil {
		log.Fatalf("Failed to load workflow definition: %v", err)
	}

	// Create the manager with the storage
	workflowReg = workflow.NewRegistry()
	workflowMgr = workflow.NewManager(workflowReg, workflowStore)

	// Load templates with custom functions
	funcMap := template.FuncMap{
		"string": func(v any) string {
			return fmt.Sprintf("%v", v)
		},
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
	}
	templates = template.Must(template.New("").Funcs(funcMap).ParseGlob("templates/*.html"))
}

// initRolesConfig initializes the role configuration with descriptions and allowed actions
func initRolesConfig() {
	rolesConfig = make(map[string]*RoleConfig)

	// Project Manager - can manage projects from planning to deployment
	rolesConfig["project_manager"] = &RoleConfig{
		Name:        "project_manager",
		Description: "Manages projects from planning to deployment. Can submit budgets, approve budgets, start development, submit legal reviews, approve final reviews, and mark projects as deployment ready.",
		Places:      []string{"planning", "budget_review", "budget_approved", "development", "design_review", "qa_testing", "security_review", "legal_review", "qa_complete", "security_complete", "legal_complete", "approved", "deployment_ready", "rejected"},
		Transitions: []string{"submit_budget", "approve_budget", "reject_budget", "start_development", "approve_standard_reviews", "approve_all_reviews_including_legal", "mark_deployment_ready", "reject_project", "restart_from_rejected"},
	}

	// Finance Manager - handles budget approvals
	rolesConfig["finance_manager"] = &RoleConfig{
		Name:        "finance_manager",
		Description: "Manages budget approvals. Reviews and approves or rejects project budgets.",
		Places:      []string{"budget_review"},
		Transitions: []string{"approve_budget", "reject_budget"},
	}

	// Developer - works on development and can submit for design review
	rolesConfig["developer"] = &RoleConfig{
		Name:        "developer",
		Description: "Develops features and submits code for design review. Works primarily in the development phase.",
		Places:      []string{"development"},
		Transitions: []string{"submit_design_review"},
	}

	// Team Lead - similar to developer but with more authority
	rolesConfig["team_lead"] = &RoleConfig{
		Name:        "team_lead",
		Description: "Leads the development team. Can submit code for design review and coordinate development activities.",
		Places:      []string{"development"},
		Transitions: []string{"submit_design_review"},
	}

	// Designer - reviews and approves/rejects designs
	rolesConfig["designer"] = &RoleConfig{
		Name:        "designer",
		Description: "Reviews design proposals and can approve or reject designs. Works in the design review phase.",
		Places:      []string{"design_review"},
		Transitions: []string{"approve_design", "reject_design"},
	}

	// Design Lead - similar to designer with approval authority
	rolesConfig["design_lead"] = &RoleConfig{
		Name:        "design_lead",
		Description: "Leads the design team. Reviews and approves/rejects designs, can proceed projects to QA and security review.",
		Places:      []string{"design_review"},
		Transitions: []string{"approve_design", "reject_design"},
	}

	// QA Tester - tests the application
	rolesConfig["tester"] = &RoleConfig{
		Name:        "tester",
		Description: "Tests the application for quality assurance. Works in QA testing phase and completes testing when coverage is sufficient.",
		Places:      []string{"qa_testing"},
		Transitions: []string{"complete_qa"},
	}

	// QA Lead - leads QA team
	rolesConfig["qa_lead"] = &RoleConfig{
		Name:        "qa_lead",
		Description: "Leads the QA team. Oversees testing and completes QA when test coverage meets requirements (>=80%).",
		Places:      []string{"qa_testing"},
		Transitions: []string{"complete_qa"},
	}

	// Security Analyst - reviews security
	rolesConfig["security_analyst"] = &RoleConfig{
		Name:        "security_analyst",
		Description: "Reviews application security. Works in security review phase and completes security reviews.",
		Places:      []string{"security_review"},
		Transitions: []string{"complete_security_review"},
	}

	// Security Lead - leads security team
	rolesConfig["security_lead"] = &RoleConfig{
		Name:        "security_lead",
		Description: "Leads the security team. Oversees security reviews and completes security assessments.",
		Places:      []string{"security_review"},
		Transitions: []string{"complete_security_review"},
	}

	// Legal Advisor - reviews legal aspects
	rolesConfig["legal_advisor"] = &RoleConfig{
		Name:        "legal_advisor",
		Description: "Reviews legal aspects of projects. Works in legal review phase and completes legal reviews.",
		Places:      []string{"legal_review"},
		Transitions: []string{"complete_legal_review"},
	}

	// Lawyer - similar to legal advisor
	rolesConfig["lawyer"] = &RoleConfig{
		Name:        "lawyer",
		Description: "Provides legal counsel. Reviews and completes legal reviews for projects requiring legal approval.",
		Places:      []string{"legal_review"},
		Transitions: []string{"complete_legal_review"},
	}

	// DevOps - handles deployment
	rolesConfig["devops"] = &RoleConfig{
		Name:        "devops",
		Description: "Handles deployment of approved projects. Can deploy projects that are ready for deployment.",
		Places:      []string{"deployment_ready"},
		Transitions: []string{"deploy"},
	}

	// Admin - full access
	rolesConfig["admin"] = &RoleConfig{
		Name:        "admin",
		Description: "Administrator with full access. Can perform all actions including approvals, rejections, and project cancellation.",
		Places:      []string{"planning", "budget_review", "budget_approved", "development", "design_review", "qa_testing", "security_review", "legal_review", "qa_complete", "security_complete", "legal_complete", "approved", "deployment_ready", "deployed", "rejected"},
		Transitions: []string{"submit_budget", "approve_budget", "reject_budget", "start_development", "submit_design_review", "submit_design_and_legal_review", "approve_design", "reject_design", "complete_qa", "complete_security_review", "complete_legal_review", "approve_standard_reviews", "approve_all_reviews_including_legal", "mark_deployment_ready", "deploy", "reject_project", "restart_from_rejected"},
	}
}

// getCurrentRole gets the current user role from session
func getCurrentRole(r *http.Request) string {
	session, _ := store.Get(r, "workflow-session")
	if role, ok := session.Values["role"].(string); ok && role != "" {
		return role
	}
	return "" // No role selected
}

// setCurrentRole sets the current user role in session
func setCurrentRole(w http.ResponseWriter, r *http.Request, role string) error {
	session, _ := store.Get(r, "workflow-session")
	session.Values["role"] = role
	return session.Save(r, w)
}

// canPerformTransition checks if a role can perform a specific transition
func canPerformTransition(role string, transitionName string) bool {
	if role == "" {
		return true // No role selected, allow all (for testing)
	}
	config, ok := rolesConfig[role]
	if !ok {
		return false
	}
	// Admin can perform all transitions
	if role == "admin" {
		return true
	}
	return slices.Contains(config.Transitions, transitionName)
}

// handleRoleSelect shows the role selection page
func handleRoleSelect(w http.ResponseWriter, r *http.Request) {
	type RoleData struct {
		Roles       map[string]*RoleConfig
		CurrentRole string
	}

	currentRole := getCurrentRole(r)
	data := RoleData{
		Roles:       rolesConfig,
		CurrentRole: currentRole,
	}

	if err := templates.ExecuteTemplate(w, "role-select.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleRoleSet sets the selected role in session
func handleRoleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse form: %v", err), http.StatusBadRequest)
		return
	}

	role := r.FormValue("role")
	if role == "" {
		http.Error(w, "Role is required", http.StatusBadRequest)
		return
	}

	if _, ok := rolesConfig[role]; !ok && role != "" {
		http.Error(w, "Invalid role", http.StatusBadRequest)
		return
	}

	if err := setCurrentRole(w, r, role); err != nil {
		http.Error(w, fmt.Sprintf("Failed to set role: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleRoleInfo returns role information as JSON
func handleRoleInfo(w http.ResponseWriter, r *http.Request) {
	role := getCurrentRole(r)
	if role == "" {
		http.Error(w, "No role selected", http.StatusNotFound)
		return
	}

	config, ok := rolesConfig[role]
	if !ok {
		http.Error(w, "Invalid role", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(config)
}

func main() {
	if err := os.MkdirAll("templates", 0755); err != nil {
		log.Printf("Warning: failed to create templates directory: %v", err)
	}

	r := chi.NewRouter()

	// Serve static files (CSS, JS, etc.)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Routes
	r.Get("/", handleHome)
	r.Get("/role/select", handleRoleSelect)
	r.Post("/role/set", handleRoleSet)
	r.Get("/role/info", handleRoleInfo)
	r.Get("/project/new", handleNewProjectForm)
	r.Post("/project/create", handleCreateProject)
	r.Route("/project", func(r chi.Router) {
		// GET and POST on /project/{id} share the same handler
		r.Get("/{id}", handleProjectPage)
		r.Post("/{id}", handleProjectPage)
	})
	r.Get("/diagram", handleDiagram)
	r.Get("/metadata", handleMetadata)

	log.Println("Advanced Workflow Server starting on :8080...")
	log.Println("Visit http://localhost:8080 to see the advanced workflow example")
	log.Fatal(http.ListenAndServe(":8080", r))
}

// ProjectSummary for list view
type ProjectSummary struct {
	ID         string
	Title      string
	State      string
	StateCount int
	Priority   string
	TeamSize   int
	CreatedAt  time.Time
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	rows, err := db.Query(`
		SELECT id, title, state, priority, team_size, created_at 
		FROM projects 
		ORDER BY created_at DESC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var summaries []ProjectSummary
	for rows.Next() {
		var summary ProjectSummary
		var stateJSON string
		var priority sql.NullString
		var teamSize sql.NullInt64
		var createdAt sql.NullTime

		if err := rows.Scan(&summary.ID, &summary.Title, &stateJSON, &priority, &teamSize, &createdAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Convert NullTime to time.Time
		if createdAt.Valid {
			summary.CreatedAt = createdAt.Time
		}

		var places []string
		if err := json.Unmarshal([]byte(stateJSON), &places); err == nil && len(places) > 0 {
			summary.State = strings.Join(places, ", ")
			summary.StateCount = len(places)
		} else {
			summary.State = "?"
		}

		if priority.Valid {
			summary.Priority = priority.String
		}
		if teamSize.Valid {
			summary.TeamSize = int(teamSize.Int64)
		}

		summaries = append(summaries, summary)
	}

	type HomeData struct {
		Projects    []ProjectSummary
		CurrentRole string
		RoleConfig  *RoleConfig
	}

	currentRole := getCurrentRole(r)
	var roleConfig *RoleConfig
	if currentRole != "" {
		roleConfig = rolesConfig[currentRole]
	}

	data := HomeData{
		Projects:    summaries,
		CurrentRole: currentRole,
		RoleConfig:  roleConfig,
	}

	if err := templates.ExecuteTemplate(w, "home.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func handleNewProjectForm(w http.ResponseWriter, r *http.Request) {
	if err := templates.ExecuteTemplate(w, "project-form.html", nil); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse form: %v", err), http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	description := strings.TrimSpace(r.FormValue("description"))
	projectType := r.FormValue("project_type")
	priority := r.FormValue("priority")
	methodology := r.FormValue("methodology")
	teamLead := strings.TrimSpace(r.FormValue("team_lead"))
	requiresLegal := r.FormValue("requires_legal") == "on"

	budgetStr := r.FormValue("budget")

	teamSizeStr := r.FormValue("team_size")

	// Validation
	if title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}
	if len(title) < 3 || len(title) > 200 {
		http.Error(w, "Title must be between 3 and 200 characters", http.StatusBadRequest)
		return
	}
	if len(description) > 1000 {
		http.Error(w, "Description must be less than 1000 characters", http.StatusBadRequest)
		return
	}
	if len(teamLead) > 100 {
		http.Error(w, "Team lead name must be less than 100 characters", http.StatusBadRequest)
		return
	}

	teamSize := 5 // Default
	if teamSizeStr != "" {
		ts, err := strconv.Atoi(teamSizeStr)
		if err != nil {
			http.Error(w, "Team size must be a valid number", http.StatusBadRequest)
			return
		}
		if ts < 1 || ts > 10 {
			http.Error(w, "Team size must be between 1 and 10", http.StatusBadRequest)
			return
		}
		teamSize = ts
	} else {
		http.Error(w, "Team size is required", http.StatusBadRequest)
		return
	}

	// Validate budget if provided
	if budgetStr != "" {
		budget, err := strconv.ParseFloat(budgetStr, 64)
		if err != nil {
			http.Error(w, "Budget must be a valid number", http.StatusBadRequest)
			return
		}
		if budget < 0 {
			http.Error(w, "Budget must be a positive number", http.StatusBadRequest)
			return
		}
	}

	id := fmt.Sprintf("project_%d", time.Now().Unix())

	// Create workflow manually so we can set context before saving
	// (CreateWorkflow saves immediately with empty context, which fails for NOT NULL fields)
	wf, err := workflow.NewWorkflow(id, workflowDef, "planning")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wf.SetManager(workflowMgr)

	// Set all custom fields BEFORE saving
	wf.SetContext("title", title)
	wf.SetContext("description", description)
	wf.SetContext("project_type", projectType)
	wf.SetContext("priority", priority)
	wf.SetContext("methodology", methodology)
	wf.SetContext("team_lead", teamLead)
	wf.SetContext("requires_legal", requiresLegal)
	// Note: budget_approved is now a workflow step (budget_review -> budget_approved), not a context value
	wf.SetContext("team_size", teamSize)

	// Set timestamps (SQLite DEFAULT doesn't work with REPLACE INTO)
	now := time.Now().Format("2006-01-02 15:04:05")
	wf.SetContext("created_at", now)
	wf.SetContext("updated_at", now)

	// Set roles for guard expressions (simulating user roles)
	wf.SetContext("roles", []string{"project_manager"})

	if budgetStr != "" {
		if budget, err := strconv.ParseFloat(budgetStr, 64); err == nil {
			wf.SetContext("budget", budget)
		}
	}

	// Save workflow with all context values set
	if err := workflowMgr.SaveWorkflow(context.Background(), id, wf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Add to registry after saving
	if err := workflowReg.AddWorkflow(wf); err != nil {
		// Log but don't fail - workflow is already saved
		log.Printf("Warning: failed to add workflow to registry: %v", err)
	}

	http.Redirect(w, r, "/project/"+id, http.StatusSeeOther)
}

type ProjectPageData struct {
	ID                 string
	Workflow           *workflow.Workflow
	Project            *Project
	Transitions        []workflow.Transition
	History            []history.TransitionRecord
	WorkflowMetadata   map[string]any
	PlaceMetadata      map[string]map[string]any
	TransitionMetadata map[string]map[string]any
	GuardErrors        map[string]string
	CurrentRole        string
	RoleConfig         *RoleConfig
}

func handleProjectPage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/project/")
	parts := strings.Split(path, "/")
	id := parts[0]

	if r.Method == "POST" {
		// Check if this is a context update request
		if r.FormValue("update_context") == "true" {
			wf, err := workflowMgr.GetWorkflow(context.Background(), id, workflowDef)
			if err != nil {
				http.Error(w, "Workflow not found", http.StatusNotFound)
				return
			}

			// Update context values from form with validation
			// Note: budget_approved is now a workflow step (budget_review -> budget_approved), not a context value
			// Budget approval is handled via submit_budget, approve_budget, and reject_budget transitions
			if teamSizeStr := r.FormValue("team_size"); teamSizeStr != "" {
				teamSize, err := strconv.Atoi(teamSizeStr)
				if err != nil {
					http.Error(w, "Team size must be a valid number", http.StatusBadRequest)
					return
				}
				if teamSize < 1 || teamSize > 10 {
					http.Error(w, "Team size must be between 1 and 10", http.StatusBadRequest)
					return
				}
				wf.SetContext("team_size", teamSize)
			}
			if rolesStr := r.FormValue("roles"); rolesStr != "" {
				// Parse comma-separated roles
				roles := strings.Split(rolesStr, ",")
				validRoles := make([]string, 0, len(roles))
				rolePattern := regexp.MustCompile("^[a-z0-9_]+$")
				for _, role := range roles {
					trimmed := strings.TrimSpace(role)
					if trimmed != "" {
						// Basic validation: only lowercase letters, numbers, and underscores
						if rolePattern.MatchString(trimmed) {
							validRoles = append(validRoles, trimmed)
						}
					}
				}
				if len(validRoles) > 0 {
					wf.SetContext("roles", validRoles)
				}
			}
			if testCoverageStr := r.FormValue("test_coverage"); testCoverageStr != "" {
				testCoverage, err := strconv.Atoi(testCoverageStr)
				if err != nil {
					http.Error(w, "Test coverage must be a valid number", http.StatusBadRequest)
					return
				}
				if testCoverage < 0 || testCoverage > 100 {
					http.Error(w, "Test coverage must be between 0 and 100", http.StatusBadRequest)
					return
				}
				wf.SetContext("test_coverage", testCoverage)
			}

			// Update timestamp
			wf.SetContext("updated_at", time.Now().Format("2006-01-02 15:04:05"))

			if err := workflowMgr.SaveWorkflow(context.Background(), id, wf); err != nil {
				http.Error(w, fmt.Sprintf("Failed to save workflow: %v", err), http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
			return
		}

		action := r.FormValue("action")
		notes := r.FormValue("notes")
		actor := r.FormValue("actor")
		if actor == "" {
			actor = "user"
		}

		wf, err := workflowMgr.GetWorkflow(context.Background(), id, workflowDef)
		if err != nil {
			http.Error(w, "Workflow not found", http.StatusNotFound)
			return
		}

		// Validate that the transition exists and is enabled
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

		// Build context with template values from form
		// Use yaml.WithTemplateValue to store values with string keys for yaml helper compatibility
		ctx := context.Background()
		if notes != "" {
			ctx = yaml.WithTemplateValue(ctx, "notes", notes)
		}
		if actor != "" {
			ctx = yaml.WithTemplateValue(ctx, "actor", actor)
		}

		// Collect template variable values from form
		// These will be used to resolve {{variable}} in YAML custom_fields
		for key := range r.Form {
			if strings.HasPrefix(key, "custom_") {
				fieldName := strings.TrimPrefix(key, "custom_")
				value := r.FormValue(key)
				if value != "" {
					// Add to context for template resolution
					// YAML custom_fields like "{{design_version}}" will resolve from this
					ctx = yaml.WithTemplateValue(ctx, fieldName, value)
				}
			}
		}

		// Add common template values
		ctx = yaml.WithTemplateValue(ctx, "user", actor)
		if methodology, ok := wf.Context("methodology"); ok {
			ctx = yaml.WithTemplateValue(ctx, "methodology", methodology)
		}
		if teamLead, ok := wf.Context("team_lead"); ok {
			ctx = yaml.WithTemplateValue(ctx, "team_lead", teamLead)
		}

		// Add request info for template resolution
		requestInfo := map[string]any{
			"ip":     r.RemoteAddr,
			"method": r.Method,
		}
		ctx = yaml.WithTemplateValue(ctx, "request", requestInfo)

		// Set workflow context values from form before validating guard
		// This is needed for guard expressions that check context values
		// For example: complete_qa requires test_coverage >= 80
		// We set these in the in-memory workflow object before validation
		if action == "complete_qa" {
			if testCoverageStr := r.FormValue("custom_test_coverage"); testCoverageStr != "" {
				testCoverage, err := strconv.Atoi(testCoverageStr)
				if err == nil && testCoverage >= 0 && testCoverage <= 100 {
					wf.SetContext("test_coverage", testCoverage)
				} else if testCoverageStr != "" {
					http.Error(w, "Test coverage must be a valid number between 0 and 100", http.StatusBadRequest)
					return
				}
			}
		}

		// Apply transition using the NEW transition-by-name API (recommended)
		// This avoids ambiguity when multiple transitions lead to the same destination
		// The YAML custom_fields will be resolved using template variables from context
		// Pass nil for overrideCustomFields to use YAML-defined fields
		err = yaml.ApplyTransitionByNameWithHistory(
			wf,
			action, // Use transition name directly instead of target places
			historyStore,
			ctx,
			notes,
			actor,
			nil, // Let YAML custom_fields handle everything via template resolution
		)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to apply transition: %v", err), http.StatusInternalServerError)
			return
		}

		// Auto-assign roles based on transition
		// This helps with guard expressions that require specific roles
		currentRoles, _ := wf.Context("roles")
		var roles []string
		if rolesList, ok := currentRoles.([]string); ok {
			roles = rolesList
		} else if rolesList, ok := currentRoles.([]any); ok {
			roles = make([]string, len(rolesList))
			for i, r := range rolesList {
				if str, ok := r.(string); ok {
					roles[i] = str
				}
			}
		} else {
			roles = []string{}
		}

		// Get target places from the transition (after it was applied)
		toPlaces := targetTransition.To()

		// Add roles based on target state
		for _, place := range toPlaces {
			switch string(place) {
			case "development":
				// Add developer and team_lead roles when entering development
				if !slices.Contains(roles, "developer") {
					roles = append(roles, "developer")
				}
				if !slices.Contains(roles, "team_lead") {
					roles = append(roles, "team_lead")
				}
			case "design_review":
				// Add designer role when entering design review
				if !slices.Contains(roles, "designer") {
					roles = append(roles, "designer")
				}
				if !slices.Contains(roles, "design_lead") {
					roles = append(roles, "design_lead")
				}
			case "qa_testing":
				// Add qa roles
				if !slices.Contains(roles, "tester") {
					roles = append(roles, "tester")
				}
				if !slices.Contains(roles, "qa_lead") {
					roles = append(roles, "qa_lead")
				}
			case "legal_review":
				// Add qa roles
				if !slices.Contains(roles, "legal_advisor") {
					roles = append(roles, "legal_advisor")
				}
				if !slices.Contains(roles, "lawyer") {
					roles = append(roles, "lawyer")
				}
			case "security_review":
				// Add security roles
				if !slices.Contains(roles, "security_analyst") {
					roles = append(roles, "security_analyst")
				}
				if !slices.Contains(roles, "security_lead") {
					roles = append(roles, "security_lead")
				}
			case "qa_complete", "security_complete":
				// Keep existing roles when completing reviews
				// No new roles needed
			case "approved":
				// Keep existing roles when approved
				// No new roles needed - will get devops role when entering deployment_ready
			case "deployment_ready":
				// Add devops role when ready to deploy
				if !slices.Contains(roles, "devops") {
					roles = append(roles, "devops")
				}
				if !slices.Contains(roles, "admin") {
					roles = append(roles, "admin")
				}
			}
		}
		wf.SetContext("roles", roles)

		// Update the updated_at timestamp
		wf.SetContext("updated_at", time.Now().Format("2006-01-02 15:04:05"))

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

	// Build project from workflow context (more reliable than direct DB query)
	project := buildProjectFromWorkflow(id, wf)
	if project == nil {
		http.Error(w, "Failed to load project data", http.StatusInternalServerError)
		return
	}

	// Get enabled transitions and check guard errors
	enabledTransitions, _ := wf.EnabledTransitions()
	guardErrors := make(map[string]string)

	// Filter transitions based on current role
	currentRole := getCurrentRole(r)
	var filteredTransitions []workflow.Transition
	for _, trans := range enabledTransitions {
		// Check if role can perform this transition
		if currentRole != "" && !canPerformTransition(currentRole, trans.Name()) {
			continue // Skip transitions this role can't perform
		}

		// Try to validate each transition to show guard errors
		// Use the new transition-by-name API for validation
		ctx := context.Background()
		if err := wf.CanTransitionWithContext(ctx, trans.Name()); err != nil {
			guardErrors[trans.Name()] = err.Error()
		}

		filteredTransitions = append(filteredTransitions, trans)
	}

	enabledTransitions = filteredTransitions

	// Load history
	historyRecords, _ := historyStore.ListHistory(context.Background(), id, history.QueryOptions{Limit: 100, Offset: 0})

	// Extract metadata
	workflowMetadata := make(map[string]any)
	if yamlConfig.Workflow.Metadata != nil {
		workflowMetadata = yamlConfig.Workflow.Metadata
	}

	placeMetadata := make(map[string]map[string]any)
	for _, place := range yamlConfig.Workflow.Places {
		if place.Metadata != nil {
			placeMetadata[place.Name] = place.Metadata
		}
	}

	transitionMetadata := make(map[string]map[string]any)
	for _, trans := range yamlConfig.Workflow.Transitions {
		if trans.Metadata != nil {
			transitionMetadata[trans.Name] = trans.Metadata
		}
	}

	var roleConfig *RoleConfig
	if currentRole != "" {
		roleConfig = rolesConfig[currentRole]
	}

	data := ProjectPageData{
		ID:                 id,
		Workflow:           wf,
		Project:            project,
		Transitions:        enabledTransitions,
		History:            historyRecords,
		WorkflowMetadata:   workflowMetadata,
		PlaceMetadata:      placeMetadata,
		TransitionMetadata: transitionMetadata,
		GuardErrors:        guardErrors,
		CurrentRole:        currentRole,
		RoleConfig:         roleConfig,
	}

	if err := templates.ExecuteTemplate(w, "project.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// buildProjectFromWorkflow builds a Project from workflow context and state
func buildProjectFromWorkflow(id string, wf *workflow.Workflow) *Project {
	project := &Project{
		ID:        id,
		State:     []string{},
		Context:   make(map[string]any),
		Metadata:  make(map[string]any),
		CreatedAt: time.Now(), // Default if not in context
		UpdatedAt: time.Now(), // Default if not in context
	}

	// Get current places
	places := wf.Marking().Places()
	for _, place := range places {
		project.State = append(project.State, string(place))
	}

	// Get all context values
	allContext := wf.AllContext()
	project.Context = allContext

	// Extract common fields from context
	if title, ok := allContext["title"].(string); ok {
		project.Title = title
	} else if titleVal := allContext["title"]; titleVal != nil {
		project.Title = fmt.Sprintf("%v", titleVal)
	}

	// Try to get created_at and updated_at from database if available
	row := db.QueryRow(`SELECT created_at, updated_at FROM projects WHERE id = ?`, id)
	var createdAt, updatedAt sql.NullTime
	if err := row.Scan(&createdAt, &updatedAt); err == nil {
		if createdAt.Valid {
			project.CreatedAt = createdAt.Time
		}
		if updatedAt.Valid {
			project.UpdatedAt = updatedAt.Time
		}
	} else {
		// If query fails (e.g., table doesn't exist yet or row not found), use defaults
		// The defaults are already set above
	}

	return project
}

func handleDiagram(w http.ResponseWriter, r *http.Request) {
	// The ?dir= switcher picks the flow orientation.
	dir := workflow.DiagramDirection(r.URL.Query().Get("dir"))
	switch dir {
	case workflow.DiagramDirectionBottomUp, workflow.DiagramDirectionLeftRight, workflow.DiagramDirectionRightLeft:
	default:
		dir = workflow.DiagramDirectionTopDown
	}
	// Render a FRESH instance built from the YAML's initial_marking (not the
	// bare definition, which cannot know where instances start): that draws
	// the ◉ start marker and lights the initial place exactly the way every
	// new project begins.
	diagram := ""
	if wf, err := yaml.NewLoader().LoadWorkflow(yamlConfig, "structure"); err == nil {
		diagram = wf.Diagram(dir)
	} else {
		diagram = workflowDef.Diagram(dir) // structure-only fallback
	}
	data := struct {
		Diagram string
		Dir     string
	}{Diagram: diagram, Dir: string(dir)}
	if err := templates.ExecuteTemplate(w, "diagram.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func handleMetadata(w http.ResponseWriter, r *http.Request) {
	type MetadataData struct {
		Workflow map[string]any
		Places   []struct {
			Name     string
			Metadata map[string]any
		}
		Transitions []struct {
			Name     string
			Metadata map[string]any
		}
	}

	data := MetadataData{
		Workflow: yamlConfig.Workflow.Metadata,
	}

	for _, place := range yamlConfig.Workflow.Places {
		data.Places = append(data.Places, struct {
			Name     string
			Metadata map[string]any
		}{
			Name:     place.Name,
			Metadata: place.Metadata,
		})
	}

	for _, trans := range yamlConfig.Workflow.Transitions {
		data.Transitions = append(data.Transitions, struct {
			Name     string
			Metadata map[string]any
		}{
			Name:     trans.Name,
			Metadata: trans.Metadata,
		})
	}

	if err := templates.ExecuteTemplate(w, "metadata.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
