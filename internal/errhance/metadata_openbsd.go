//go:build openbsd

package errhance

// OpenBSD exposes neither native extended ACLs nor xattrs. POSIX permissions,
// ownership, and file flags are checked by the shared BSD implementation.
// Remote server-side access policy is not migrated by this tool.
func bsdReadAttrs(_ int) (map[string]string, error) { return nil, nil }

func bsdApplyAttrs(_ int, _, _ map[string]string) error { return nil }
