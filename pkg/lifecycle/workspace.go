package lifecycle

// WorkspaceState represents the lifecycle state of a hosted workspace.
type WorkspaceState string

// Workspace lifecycle states.
const (
	WorkspacePendingPlacement WorkspaceState = "pending-placement"
	WorkspaceCreating         WorkspaceState = "creating"
	WorkspaceActive           WorkspaceState = "active"
	WorkspaceSuspending       WorkspaceState = "suspending"
	WorkspaceSuspended        WorkspaceState = "suspended"
	WorkspaceResuming         WorkspaceState = "resuming"
	WorkspaceFailed           WorkspaceState = "failed"
	WorkspaceDeleting         WorkspaceState = "deleting"
	WorkspaceDeleted          WorkspaceState = "deleted"
)

var workspaceTransitions = []Transition[WorkspaceState]{
	{WorkspacePendingPlacement, WorkspaceCreating, "placement resolved"},
	{WorkspacePendingPlacement, WorkspaceFailed, "placement failed"},
	{WorkspaceCreating, WorkspaceActive, "creation completed"},
	{WorkspaceCreating, WorkspaceFailed, "creation failed"},
	{WorkspaceActive, WorkspaceSuspending, "suspend requested"},
	{WorkspaceActive, WorkspaceDeleting, "delete requested"},
	{WorkspaceActive, WorkspaceFailed, "runtime error"},
	{WorkspaceSuspending, WorkspaceSuspended, "suspension completed"},
	{WorkspaceSuspending, WorkspaceFailed, "suspension failed"},
	{WorkspaceSuspended, WorkspaceResuming, "resume requested"},
	{WorkspaceSuspended, WorkspaceDeleting, "delete requested"},
	{WorkspaceResuming, WorkspaceActive, "resume completed"},
	{WorkspaceResuming, WorkspaceFailed, "resume failed"},
	{WorkspaceFailed, WorkspaceDeleting, "delete requested"},
	{WorkspaceFailed, WorkspaceResuming, "retry requested"},
	{WorkspaceDeleting, WorkspaceDeleted, "deletion completed"},
	// Note: deleting→failed→resuming→active is intentionally allowed.
	// Callers must implement their own retry cap / circuit breaker.
	{WorkspaceDeleting, WorkspaceFailed, "deletion failed"},
}

var workspaceTransitionSet = buildTransitionSet(workspaceTransitions)

// ValidWorkspaceTransitions returns all valid workspace state transitions.
func ValidWorkspaceTransitions() []Transition[WorkspaceState] {
	out := make([]Transition[WorkspaceState], len(workspaceTransitions))
	copy(out, workspaceTransitions)
	return out
}

// CanTransitionWorkspace reports whether transitioning from one workspace
// state to another is valid.
func CanTransitionWorkspace(from, to WorkspaceState) bool {
	return workspaceTransitionSet[transitionKey{string(from), string(to)}]
}

// TransitionWorkspace validates and performs a workspace state transition.
// Returns the new state or an InvalidTransitionError.
func TransitionWorkspace(from, to WorkspaceState) (WorkspaceState, error) {
	if !CanTransitionWorkspace(from, to) {
		return from, &InvalidTransitionError{Entity: "workspace", From: string(from), To: string(to)}
	}
	return to, nil
}

// WorkspaceStateCategory returns the category for a workspace state.
func WorkspaceStateCategory(s WorkspaceState) StateCategory {
	switch s {
	case WorkspaceActive, WorkspaceSuspended:
		return CategoryActive
	case WorkspacePendingPlacement, WorkspaceCreating, WorkspaceSuspending, WorkspaceResuming, WorkspaceDeleting:
		return CategoryTransitioning
	case WorkspaceDeleted:
		return CategoryTerminal
	case WorkspaceFailed:
		return CategoryError
	default:
		return CategoryError
	}
}

// IsTerminalWorkspace reports whether the workspace state is terminal
// (no further transitions possible by design).
func IsTerminalWorkspace(s WorkspaceState) bool {
	return s == WorkspaceDeleted
}
