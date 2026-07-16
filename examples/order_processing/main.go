// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/ehabterra/workflow"
)

// Order represents an order in the system
type Order struct {
	ID         string
	CustomerID string
	Amount     float64
	Items      []OrderItem
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// OrderItem represents an item in an order
type OrderItem struct {
	ProductID string
	Quantity  int
	Price     float64
}

// Inventory represents the inventory system
type Inventory struct {
	items map[string]int
}

// PaymentProcessor represents the payment processing system
type PaymentProcessor struct {
	successRate float64
}

func main() {
	// Initialize systems
	inventory := NewInventory()
	paymentProcessor := NewPaymentProcessor(0.8) // 80% success rate

	// Define order places
	places := []workflow.Place{
		"pending",
		"payment_processing",
		"payment_approved",
		"payment_failed",
		"inventory_check",
		"inventory_insufficient",
		"shipping",
		"delivered",
		"cancelled",
		"refunded",
	}

	// Define transitions with more comprehensive flow
	transitions := []workflow.Transition{
		createTransition("process_payment", []workflow.Place{"pending"}, []workflow.Place{"payment_processing"}),
		createTransition("payment_success", []workflow.Place{"payment_processing"}, []workflow.Place{"payment_approved"}),
		createTransition("payment_failure", []workflow.Place{"payment_processing"}, []workflow.Place{"payment_failed"}),
		createTransition("retry_payment", []workflow.Place{"payment_failed"}, []workflow.Place{"payment_processing"}),
		createTransition("check_inventory", []workflow.Place{"payment_approved"}, []workflow.Place{"inventory_check"}),
		createTransition("inventory_available", []workflow.Place{"inventory_check"}, []workflow.Place{"shipping"}),
		createTransition("inventory_insufficient", []workflow.Place{"inventory_check"}, []workflow.Place{"inventory_insufficient"}),
		createTransition("restock_and_ship", []workflow.Place{"inventory_insufficient"}, []workflow.Place{"shipping"}),
		createTransition("mark_delivered", []workflow.Place{"shipping"}, []workflow.Place{"delivered"}),
		createTransition("cancel_pending", []workflow.Place{"pending"}, []workflow.Place{"cancelled"}),
		createTransition("cancel_payment_processing", []workflow.Place{"payment_processing"}, []workflow.Place{"cancelled"}),
		createTransition("cancel_payment_approved", []workflow.Place{"payment_approved"}, []workflow.Place{"cancelled"}),
		createTransition("refund_order", []workflow.Place{"delivered"}, []workflow.Place{"refunded"}),
	}

	// Create workflow definition
	definition, err := workflow.NewDefinition(places, transitions)
	if err != nil {
		log.Fatal(err)
	}

	// Create workflow instance
	wf, err := workflow.NewWorkflow("order-123", definition, "pending")
	if err != nil {
		log.Fatal(err)
	}

	// Create sample order
	order := &Order{
		ID:         "order-123",
		CustomerID: "cust-456",
		Amount:     150.0,
		Items: []OrderItem{
			{ProductID: "prod-001", Quantity: 2, Price: 50.0},
			{ProductID: "prod-002", Quantity: 1, Price: 50.0},
		},
		Status:    "pending",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Add order context
	wf.SetContext("order", order)
	wf.SetContext("order_amount", order.Amount)
	wf.SetContext("customer_id", order.CustomerID)

	// Add comprehensive event listeners for business logic
	wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error {
		log.Printf("🔄 Processing transition: %s", event.Transition().Name())

		// Add business logic based on transition
		switch event.Transition().Name() {
		case "process_payment":
			return handlePaymentProcessing(wf, paymentProcessor)
		case "check_inventory":
			return handleInventoryCheck(wf, inventory)
		case "mark_delivered":
			return handleDelivery(wf)
		}

		return nil
	})

	wf.AddEventListener(workflow.EventAfterTransition, func(event workflow.Event) error {
		log.Printf("✅ Completed transition: %s", event.Transition().Name())

		// Update order status
		if orderValue, ok := wf.Context("order"); ok {
			if order, ok := orderValue.(*Order); ok {
				order.Status = string(wf.CurrentPlaces()[0])
				order.UpdatedAt = time.Now()
			}
		}

		return nil
	})

	// Demonstrate the complete order processing workflow
	fmt.Println("🚀 Starting Order Processing Workflow")
	fmt.Println("=====================================")

	// Step 1: Process payment
	fmt.Println("\n1. Processing payment...")
	if err := wf.Apply([]workflow.Place{"payment_processing"}); err != nil {
		log.Printf("❌ Payment processing failed: %v", err)
		return
	}

	// Simulate payment result
	if paymentProcessor.ProcessPayment(order.Amount) {
		fmt.Println("✅ Payment approved")
		if err := wf.Apply([]workflow.Place{"payment_approved"}); err != nil {
			log.Printf("❌ Payment approval failed: %v", err)
			return
		}
	} else {
		fmt.Println("❌ Payment failed")
		if err := wf.Apply([]workflow.Place{"payment_failed"}); err != nil {
			log.Printf("❌ Payment failure handling failed: %v", err)
			return
		}

		// Retry payment
		fmt.Println("🔄 Retrying payment...")
		if err := wf.Apply([]workflow.Place{"payment_processing"}); err != nil {
			log.Printf("❌ Payment retry failed: %v", err)
			return
		}

		if paymentProcessor.ProcessPayment(order.Amount) {
			fmt.Println("✅ Payment approved on retry")
			if err := wf.Apply([]workflow.Place{"payment_approved"}); err != nil {
				log.Printf("❌ Payment approval failed: %v", err)
				return
			}
		} else {
			fmt.Println("❌ Payment failed on retry - cancelling order")
			if err := wf.Apply([]workflow.Place{"cancelled"}); err != nil {
				log.Printf("❌ Order cancellation failed: %v", err)
			}
			return
		}
	}

	// Step 2: Check inventory
	fmt.Println("\n2. Checking inventory...")
	if err := wf.Apply([]workflow.Place{"inventory_check"}); err != nil {
		log.Printf("❌ Inventory check failed: %v", err)
		return
	}

	// Step 3: Process inventory result
	if inventory.HasSufficientStock(order.Items) {
		fmt.Println("✅ Inventory available")
		if err := wf.Apply([]workflow.Place{"shipping"}); err != nil {
			log.Printf("❌ Shipping transition failed: %v", err)
			return
		}
	} else {
		fmt.Println("⚠️  Insufficient inventory")
		if err := wf.Apply([]workflow.Place{"inventory_insufficient"}); err != nil {
			log.Printf("❌ Inventory insufficient handling failed: %v", err)
			return
		}

		// Restock and continue
		fmt.Println("📦 Restocking inventory...")
		inventory.Restock(order.Items)
		if err := wf.Apply([]workflow.Place{"shipping"}); err != nil {
			log.Printf("❌ Shipping after restock failed: %v", err)
			return
		}
	}

	// Step 4: Mark as delivered
	fmt.Println("\n3. Marking as delivered...")
	if err := wf.Apply([]workflow.Place{"delivered"}); err != nil {
		log.Printf("❌ Delivery marking failed: %v", err)
		return
	}

	// Final status
	fmt.Println("\n📊 Final Order Status:")
	fmt.Printf("Order ID: %s\n", order.ID)
	fmt.Printf("Customer: %s\n", order.CustomerID)
	fmt.Printf("Amount: $%.2f\n", order.Amount)
	fmt.Printf("Status: %s\n", order.Status)
	fmt.Printf("Current places: %v\n", wf.CurrentPlaces())

	// Generate and display workflow diagram
	fmt.Println("\n📋 Workflow Diagram:")
	fmt.Println("===================")
	diagram := wf.Diagram()
	fmt.Println(diagram)

	// Demonstrate transition history
	fmt.Println("\n📜 Transition History:")
	fmt.Println("=====================")
	fmt.Println("Note: Transition history tracking is available in the website workflow example")
	fmt.Println("This demonstrates the workflow state progression through the order processing steps.")
}

// NewInventory creates a new inventory system
func NewInventory() *Inventory {
	return &Inventory{
		items: map[string]int{
			"prod-001": 5,
			"prod-002": 3,
		},
	}
}

// HasSufficientStock checks if there's enough stock for the order
func (i *Inventory) HasSufficientStock(items []OrderItem) bool {
	for _, item := range items {
		if i.items[item.ProductID] < item.Quantity {
			return false
		}
	}
	return true
}

// Restock adds inventory for the given items
func (i *Inventory) Restock(items []OrderItem) {
	for _, item := range items {
		i.items[item.ProductID] += item.Quantity + 2 // Add extra stock
	}
}

// NewPaymentProcessor creates a new payment processor
func NewPaymentProcessor(successRate float64) *PaymentProcessor {
	return &PaymentProcessor{
		successRate: successRate,
	}
}

// ProcessPayment simulates payment processing
func (p *PaymentProcessor) ProcessPayment(amount float64) bool {
	// Simulate processing time
	time.Sleep(100 * time.Millisecond)

	// Simulate success/failure based on success rate
	return rand.Float64() < p.successRate
}

// handlePaymentProcessing handles payment processing logic
func handlePaymentProcessing(wf *workflow.Workflow, _ *PaymentProcessor) error {
	if amountValue, ok := wf.Context("order_amount"); ok {
		if amount, ok := amountValue.(float64); ok {
			log.Printf("Processing payment for amount: $%.2f", amount)
		}
	}
	return nil
}

// handleInventoryCheck handles inventory checking logic
func handleInventoryCheck(wf *workflow.Workflow, _ *Inventory) error {
	if orderValue, ok := wf.Context("order"); ok {
		if order, ok := orderValue.(*Order); ok {
			log.Printf("Checking inventory for %d items", len(order.Items))
		}
	}
	return nil
}

// handleDelivery handles delivery logic
func handleDelivery(wf *workflow.Workflow) error {
	if orderValue, ok := wf.Context("order"); ok {
		if order, ok := orderValue.(*Order); ok {
			log.Printf("Delivering order %s to customer %s", order.ID, order.CustomerID)
		}
	}
	return nil
}

func createTransition(name string, from, to []workflow.Place) workflow.Transition {
	tr, err := workflow.NewTransition(name, from, to)
	if err != nil {
		log.Fatal(err)
	}
	return *tr
}
