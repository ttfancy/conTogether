package domain

// Visibility controls whether a resource (Container, Upload) can be
// read by any authenticated caller (Public) or only by its owner
// (Private, the default) — mutation (start/stop/delete, etc.) always
// stays owner-only regardless of visibility.
type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

// Valid reports whether v is a recognized visibility value.
func (v Visibility) Valid() bool {
	return v == VisibilityPrivate || v == VisibilityPublic
}
