package backend

// implemented by backends that require one-time setup (e.g. local: create subdirs)
type Setupper interface {
	Setup() error
}
