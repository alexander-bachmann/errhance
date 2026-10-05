//go:build netbsd

package errhance

// NetBSD has no native extended ACL API; extattr namespaces are checked by
// bsdReadAttrs. Remote server-side access policy is not migrated by this tool.
func bsdCheckACL(_ int) error { return nil }
