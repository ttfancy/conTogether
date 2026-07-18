package domain

import "time"

// Upload is the persisted metadata record for a file saved via
// upload.Service — distinct from the file itself, which lives on disk
// at Path. Owner/Visibility mirror Container's model so both resource
// types authorize reads the same way (owner, or anyone if public).
type Upload struct {
	ID          string
	OwnerID     string
	Filename    string
	Path        string
	ContentType string
	Size        int64
	Visibility  Visibility
	CreatedAt   time.Time
}
