package backend

// Setupper is implemented by backends that need initialization before first use
// (e.g. local: create subdirectories; S3: verify bucket exists).
type Setupper interface {
	Setup() error
}
