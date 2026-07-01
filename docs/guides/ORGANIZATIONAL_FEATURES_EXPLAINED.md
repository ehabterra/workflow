# Organizational Structure & Assignment Management Explained

This document explains what **organizational structure** and **assignment management** mean in workflow engines, why petri_flow has them, and why we're not planning them for our core engine.

## What Are These Features?

### 1. Organizational Structure

**Definition**: A way to model your company's organizational hierarchy and relationships.

**What it includes**:
- **Roles**: Job functions (e.g., "Manager", "Developer", "Designer")
- **Groups**: Teams or departments (e.g., "Engineering Team", "Marketing Department")
- **Positions**: Specific job positions in the org chart (e.g., "Senior Developer", "VP of Engineering")
- **Departments**: Organizational units (e.g., "Engineering", "Sales", "HR")
- **Hierarchy**: Relationships between these (e.g., "Manager" reports to "Director")

**Example in petri_flow**:
```ruby
# Define organizational structure
module Wf
  class User < ApplicationRecord
    belongs_to :group, optional: true
    include Wf::ActsAsParty
    acts_as_party(user: true, party_name: :name)
  end

  class Group < ApplicationRecord
    has_many :users
    include Wf::ActsAsParty
    acts_as_party(user: false, party_name: :name)
  end
end
```

**Real-world example**:
```
Company: Acme Corp
├── Department: Engineering
│   ├── Group: Backend Team
│   │   ├── Position: Senior Developer
│   │   │   └── User: Alice (has role: developer)
│   │   └── Position: Team Lead
│   │       └── User: Bob (has role: team_lead)
│   └── Group: Frontend Team
│       └── ...
└── Department: Sales
    └── ...
```

### 2. Assignment Management

**Definition**: A system for automatically assigning workflow tasks to the right people based on organizational rules.

**What it includes**:
- **Automatic Assignment**: Assign tasks to users based on role, group, or position
- **Assignment Rules**: Define who can work on what (e.g., "Assign to any developer in the Backend Team")
- **Assignment History**: Track who was assigned what and when
- **Reassignment**: Change assignments dynamically
- **Load Balancing**: Distribute work evenly across team members

**Example in petri_flow**:
```ruby
# Assignment rule: "Assign to any developer in the Backend Team"
transition.assign_to do |workflow, transition|
  Group.find_by(name: "Backend Team").users.where(role: "developer")
end
```

**Real-world example**:
```
Workflow: Code Review
Place: "needs_review"
Assignment Rule: "Assign to any developer in the Backend Team"

Result:
- Task automatically assigned to: Alice (Backend Team, Developer)
- If Alice is busy, assign to: Charlie (Backend Team, Developer)
- If no one available, queue for next available developer
```

## Why Does petri_flow Have These Features?

**petri_flow** is designed for **enterprise workflow automation** where:

1. **Large Organizations**: Companies with complex org structures
2. **Role-Based Workflows**: Tasks need to be assigned to specific roles
3. **Team Management**: Work needs to be distributed across teams
4. **Compliance**: Need to track who did what for audit purposes

**Example Use Case**:
```
Company: Large Bank
Workflow: Loan Approval

Organizational Structure:
- Department: Risk Management
  - Group: Senior Analysts
    - Role: Senior Risk Analyst
  - Group: Junior Analysts
    - Role: Risk Analyst

Assignment Rules:
- Loans < $10,000 → Assign to Junior Analyst
- Loans >= $10,000 → Assign to Senior Analyst
- If Senior Analyst unavailable → Escalate to Director

Benefits:
- Automatic task routing
- Compliance tracking
- Workload distribution
- Clear accountability
```

## What We Currently Have (Simpler Approach)

### Current Implementation

We use a **simpler, role-based approach** that's sufficient for most use cases:

**1. Role-Based Guards** (Implemented):
```yaml
transitions:
  - name: approve_budget
    from: [budget_review]
    to: [budget_approved]
    guard: "hasRole('finance_manager') or hasRole('admin')"
```

**2. Roles in Context** (Implemented):
```go
wf.SetContext("roles", []string{"developer", "team_lead"})
```

**3. hasRole() Helper** (Implemented):
```go
// In expression.go
env["hasRole"] = func(role string) bool {
    roles, ok := wf.Context("roles")
    // Check if role exists in roles list
    return slices.Contains(roles, role)
}
```

**Example from our advanced_workflow**:
```go
// Roles are stored in workflow context
wf.SetContext("roles", []string{"developer", "team_lead"})

// Guards check roles
guard: "hasRole('developer') or hasRole('team_lead')"
```

### What This Gives Us

✅ **Role-based access control**: Can check if user has required role
✅ **Flexible**: Roles stored in context, can be changed dynamically
✅ **Simple**: No complex organizational structure needed
✅ **Sufficient for most cases**: Works for 90% of use cases

### What This Doesn't Give Us

❌ **Automatic assignment**: Can't automatically assign tasks to users
❌ **Team management**: Can't route to "any developer in Backend Team"
❌ **Load balancing**: Can't distribute work across team members
❌ **Organizational hierarchy**: Can't model company structure
❌ **Position tracking**: Can't track specific job positions

## Why We're Not Planning These Features

### 1. **Design Philosophy: Core Engine vs Full Platform**

**Our Goal**: Build a **core workflow engine** that's:
- Framework-agnostic
- Standalone library
- Focused on Petri Net/CPN capabilities
- Flexible and extensible

**petri_flow's Goal**: Build a **full workflow platform** that includes:
- Organizational management
- Assignment system
- Form builder
- Web UI
- Rails integration

**Analogy**:
- **petri_flow**: Like a complete CRM system (Salesforce)
- **Our engine**: Like a database engine (PostgreSQL) - powerful core, others build on top

### 2. **Separation of Concerns**

**Organizational Structure** belongs in:
- Your application's user management system
- Your HR/org chart system
- Your identity provider (LDAP, Active Directory, etc.)

**Assignment Management** belongs in:
- Your task management system
- Your notification system
- Your user interface

**Workflow Engine** should focus on:
- State management
- Transition logic
- Token flow
- Guard evaluation

### 3. **Flexibility Over Prescription**

**Our Approach**: Provide building blocks, let users build what they need:

```go
// User can implement their own assignment logic
func assignTask(workflow *Workflow, transition string) {
    // Get organizational data from your system
    team := getTeamFromOrgSystem("Backend Team")
    availableDevelopers := getAvailableUsers(team, "developer")
    
    // Assign to first available
    assignee := availableDevelopers[0]
    
    // Set in workflow context
    workflow.SetContext("assigned_to", assignee.ID)
    workflow.SetContext("roles", assignee.Roles)
}
```

**petri_flow's Approach**: Built-in organizational system:

```ruby
# Built into the engine
transition.assign_to do |workflow, transition|
  Group.find_by(name: "Backend Team").users.where(role: "developer")
end
```

**Our Advantage**: 
- Works with any organizational system
- Not tied to specific data models
- More flexible for different use cases

### 4. **Complexity vs Value**

**Organizational Structure Complexity**:
- Requires database schema for org structure
- Needs to handle hierarchy changes
- Must sync with external systems
- Adds significant complexity

**Value for Most Users**:
- Many workflows don't need complex org structure
- Simple roles are sufficient
- Can be built on top if needed

**Our Decision**: Keep it simple, let users extend if needed.

## How to Achieve Similar Functionality (Without Built-in Features)

### 1. Simple Role-Based System (Current)

```yaml
# workflow.yaml
transitions:
  - name: approve_budget
    guard: "hasRole('finance_manager')"
```

```go
// Your application code
user := getCurrentUser()
wf.SetContext("roles", user.Roles)
wf.ApplyTransition("approve_budget")
```

### 2. Team-Based Assignment (Custom Implementation)

```go
// Your application code
func assignToTeam(workflow *Workflow, teamName string) {
    // Get team from your org system
    team := orgSystem.GetTeam(teamName)
    
    // Get available members
    members := team.GetAvailableMembers()
    
    // Assign to first available
    assignee := members[0]
    
    // Update workflow
    workflow.SetContext("assigned_to", assignee.ID)
    workflow.SetContext("roles", assignee.Roles)
    workflow.SetContext("team", teamName)
}
```

### 3. Department-Based Routing (Custom Implementation)

```go
// Your application code
func routeByDepartment(workflow *Workflow, department string) {
    // Get department from your org system
    dept := orgSystem.GetDepartment(department)
    
    // Get department members with required role
    members := dept.GetMembersWithRole("manager")
    
    // Assign
    assignee := selectAssignee(members) // Your selection logic
    
    workflow.SetContext("assigned_to", assignee.ID)
    workflow.SetContext("department", department)
}
```

### 4. Load Balancing (Custom Implementation)

```go
// Your application code
func assignWithLoadBalancing(workflow *Workflow, role string) {
    // Get all users with role
    users := orgSystem.GetUsersWithRole(role)
    
    // Get workload for each
    workloads := make(map[string]int)
    for _, user := range users {
        workloads[user.ID] = getWorkload(user.ID)
    }
    
    // Assign to user with least workload
    assignee := findLeastLoaded(workloads)
    
    workflow.SetContext("assigned_to", assignee.ID)
}
```

## Comparison: Built-in vs Custom

| Aspect | Built-in (petri_flow) | Custom (Our Approach) |
|--------|----------------------|----------------------|
| **Setup Complexity** | High (need to model org structure) | Low (just use roles) |
| **Flexibility** | Limited to petri_flow's model | Unlimited (your org system) |
| **Integration** | Must use petri_flow's org system | Works with any org system |
| **Maintenance** | petri_flow maintains it | You maintain it |
| **Use Cases** | Good for standard org structures | Good for custom/unique structures |

## When You Might Need These Features

### You DON'T Need Them If:
- ✅ Simple role-based access is sufficient
- ✅ You have < 100 users
- ✅ Org structure is simple (flat or 2 levels)
- ✅ Assignment logic is straightforward

### You MIGHT Need Them If:
- ⚠️ Complex organizational hierarchy (5+ levels)
- ⚠️ Dynamic team assignments
- ⚠️ Load balancing across large teams
- ⚠️ Compliance requirements for org tracking

### You CAN Build Them If Needed:
- ✅ Use our workflow context for assignment data
- ✅ Implement assignment logic in your application
- ✅ Integrate with your existing org system
- ✅ Use event listeners for automatic assignment

## Example: Building Assignment on Top

Here's how you could build assignment management using our engine:

```go
// assignment.go - Your custom assignment system
package assignment

import (
    "github.com/ehabterra/workflow"
    "your-org-system/org"
)

// AssignToTeam assigns workflow task to team members
func AssignToTeam(wf *workflow.Workflow, teamName string, role string) error {
    // Get team from your organizational system
    team, err := org.GetTeam(teamName)
    if err != nil {
        return err
    }
    
    // Get available team members with role
    members := team.GetAvailableMembers(role)
    if len(members) == 0 {
        return fmt.Errorf("no available members in team %s with role %s", teamName, role)
    }
    
    // Select assignee (your logic: round-robin, least loaded, etc.)
    assignee := selectAssignee(members)
    
    // Update workflow context
    wf.SetContext("assigned_to", assignee.ID)
    wf.SetContext("assigned_to_name", assignee.Name)
    wf.SetContext("roles", assignee.Roles)
    wf.SetContext("team", teamName)
    wf.SetContext("department", team.Department)
    
    return nil
}

// AutoAssign listens to workflow events and assigns automatically
func AutoAssign(wf *workflow.Workflow) {
    wf.AddEventListener(workflow.EventAfterTransition, func(event workflow.Event) error {
        transition := event.Transition()
        
        // Your assignment rules
        switch transition.Name() {
        case "submit_for_review":
            return AssignToTeam(wf, "Backend Team", "developer")
        case "approve_budget":
            return AssignToTeam(wf, "Finance", "finance_manager")
        }
        
        return nil
    })
}
```

## Summary

### What We're NOT Planning:
- ❌ **Organizational structure**: Complex org hierarchy modeling
- ❌ **Assignment management**: Automatic task assignment system

### Why We're NOT Planning Them:
1. **Design Philosophy**: Core engine, not full platform
2. **Separation of Concerns**: Belongs in application layer
3. **Flexibility**: Users can build what they need
4. **Complexity**: Adds significant complexity for limited value

### What We DO Provide:
- ✅ **Role-based guards**: `hasRole('developer')`
- ✅ **Workflow context**: Store assignment data
- ✅ **Event system**: Hook into transitions for custom assignment
- ✅ **Flexibility**: Build assignment logic in your application

### What You CAN Build:
- ✅ Team-based assignment (using your org system)
- ✅ Department-based routing (using your org system)
- ✅ Load balancing (using your task system)
- ✅ Any custom assignment logic (using workflow context)

**Bottom Line**: We provide the **workflow engine core**. You build the **organizational and assignment logic** on top, giving you maximum flexibility to integrate with your existing systems.

