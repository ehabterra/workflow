// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"time"

	"github.com/ehabterra/workflow"
)

// Document represents a document in the approval process
type Document struct {
	ID          string
	Title       string
	Content     string
	Author      string
	CreatedAt   time.Time
	Approvals   map[string]bool
	Rejections  map[string]bool
	Comments    map[string][]string
	FinalStatus string
}

// DocumentContext holds the document and approval context
type DocumentContext struct {
	Document *Document
	User     string
	Role     string
}

func main() {
	// Define the workflow states
	states := []workflow.Place{
		"draft",
		"pending_technical_review",
		"technical_approved",
		"pending_legal_review",
		"legal_approved",
		"pending_manager_approval",
		"pending_director_approval",
		"approved",
		"rejected",
		"archived",
	}

	// Create transitions
	transitions := []workflow.Transition{
		// Initial submission
		*createTransition("submit_for_review", []workflow.Place{"draft"}, []workflow.Place{"pending_technical_review", "pending_legal_review"}),

		// Technical review paths
		*createTransition("technical_approve", []workflow.Place{"pending_technical_review"}, []workflow.Place{"technical_approved"}),
		*createTransition("technical_reject", []workflow.Place{"pending_technical_review"}, []workflow.Place{"rejected"}),

		// Legal review paths
		*createTransition("legal_approve", []workflow.Place{"pending_legal_review"}, []workflow.Place{"legal_approved"}),
		*createTransition("legal_reject", []workflow.Place{"pending_legal_review"}, []workflow.Place{"rejected"}),

		*createTransition("pending_manager_approve", []workflow.Place{"technical_approved", "legal_approved"}, []workflow.Place{"pending_manager_approval"}),

		// Manager approval paths
		*createTransition("manager_approve", []workflow.Place{"pending_manager_approval"}, []workflow.Place{"pending_director_approval"}),
		*createTransition("manager_reject", []workflow.Place{"pending_manager_approval"}, []workflow.Place{"rejected"}),

		// Director approval paths
		*createTransition("director_approve", []workflow.Place{"pending_director_approval"}, []workflow.Place{"approved"}),
		*createTransition("director_reject", []workflow.Place{"pending_director_approval"}, []workflow.Place{"rejected"}),

		// Archive paths
		*createTransition("archive_approved", []workflow.Place{"approved"}, []workflow.Place{"archived"}),
		*createTransition("archive_rejected", []workflow.Place{"rejected"}, []workflow.Place{"archived"}),

		// Reopen paths
		*createTransition("reopen_approved", []workflow.Place{"approved"}, []workflow.Place{"draft"}),
		*createTransition("reopen_rejected", []workflow.Place{"rejected"}, []workflow.Place{"draft"}),
	}

	// Create workflow definition
	definition, err := workflow.NewDefinition(states, transitions)
	if err != nil {
		panic(err)
	}

	// Create workflow instance
	wf, err := workflow.NewWorkflow("document_approval", definition, "draft")
	if err != nil {
		panic(err)
	}

	// Add guard event listener
	wf.AddGuardEventListener(func(event *workflow.GuardEvent) error {
		ctxValue, ok := event.Workflow().Context("document_context")
		if ok && ctxValue == nil {
			return fmt.Errorf("missing document context")
		}
		ctx, ok := ctxValue.(*DocumentContext)
		if !ok {
			return fmt.Errorf("invalid document context type")
		}

		// Check if transitioning from pending_manager_approval
		for _, from := range event.From() {
			if from == "pending_manager_approval" {
				// Check if both technical and legal approvals exist
				if !ctx.Document.Approvals["technical_reviewer"] || !ctx.Document.Approvals["legal_reviewer"] {
					event.SetBlocking(true)
					return fmt.Errorf("both technical and legal approvals are required before manager decision")
				}
			}
		}
		return nil
	})

	// Add event listeners
	wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error {
		ctxValue, ok := event.Workflow().Context("document_context")
		if !ok {
			return fmt.Errorf("missing document context")
		}
		ctx, ok := ctxValue.(*DocumentContext)
		if !ok {
			return fmt.Errorf("invalid document context type")
		}

		// Log the transition
		fmt.Printf("Document %s: %s -> %v by %s (%s)\n",
			ctx.Document.ID,
			event.From(),
			event.To(),
			ctx.User,
			ctx.Role,
		)
		return nil
	})

	wf.AddEventListener(workflow.EventAfterTransition, func(event workflow.Event) error {
		ctxValue, ok := event.Workflow().Context("document_context")
		if !ok {
			return fmt.Errorf("missing document context")
		}
		ctx, ok := ctxValue.(*DocumentContext)
		if !ok {
			return fmt.Errorf("invalid document context type")
		}

		// Update document state
		for _, state := range event.To() {
			switch state {
			case "approved":
				ctx.Document.FinalStatus = "approved"
			case "rejected":
				ctx.Document.FinalStatus = "rejected"
			case "archived":
				ctx.Document.FinalStatus = "archived"
			}
		}

		// Check if we need to automatically trigger pending_manager_approve
		currentPlaces := wf.CurrentPlaces()
		hasTechnicalApproved := false
		hasLegalApproved := false
		for _, place := range currentPlaces {
			if place == "technical_approved" {
				hasTechnicalApproved = true
			}
			if place == "legal_approved" {
				hasLegalApproved = true
			}
		}

		// If both approvals are present, trigger the pending_manager_approve transition
		if hasTechnicalApproved && hasLegalApproved {
			ctx.User = "system"
			ctx.Role = "system"
			err := wf.Apply([]workflow.Place{"pending_manager_approval"})
			if err != nil {
				return fmt.Errorf("failed to auto-trigger pending_manager_approve: %w", err)
			}
		}

		return nil
	})

	// Create a sample document
	doc := &Document{
		ID:         "DOC-001",
		Title:      "Technical Specification",
		Content:    "This is a sample technical specification document.",
		Author:     "John Doe",
		CreatedAt:  time.Now(),
		Approvals:  make(map[string]bool),
		Rejections: make(map[string]bool),
		Comments:   make(map[string][]string),
	}

	// Simulate the approval process
	simulateApprovalProcess(wf, doc)

	// Print the final workflow diagram
	fmt.Println("\nWorkflow Diagram:")
	fmt.Println(wf.Diagram())
}

func createTransition(name string, from, to []workflow.Place) *workflow.Transition {
	tr, err := workflow.NewTransition(name, from, to)
	if err != nil {
		panic(err)
	}
	return tr
}

func simulateApprovalProcess(wf *workflow.Workflow, doc *Document) {
	// Submit for review
	ctx := &DocumentContext{
		Document: doc,
		User:     doc.Author,
		Role:     "author",
	}
	wf.SetContext("document_context", ctx)
	err := wf.Apply([]workflow.Place{"pending_technical_review", "pending_legal_review"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// Technical review
	ctx.User = "tech_reviewer"
	ctx.Role = "technical_reviewer"
	doc.Approvals["technical_reviewer"] = true
	err = wf.Apply([]workflow.Place{"technical_approved"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// Legal review
	ctx.User = "legal_reviewer"
	ctx.Role = "legal_reviewer"
	doc.Approvals["legal_reviewer"] = true
	err = wf.Apply([]workflow.Place{"legal_approved"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// Manager approval
	ctx.User = "manager"
	ctx.Role = "manager"
	err = wf.Apply([]workflow.Place{"pending_director_approval"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// Director approval
	ctx.User = "director"
	ctx.Role = "director"
	err = wf.Apply([]workflow.Place{"approved"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// Archive
	ctx.User = "admin"
	ctx.Role = "administrator"
	err = wf.Apply([]workflow.Place{"archived"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
}
