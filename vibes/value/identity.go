package value

// SliceIdentity captures the identity of a slice header so cycle
// detection in value graphs can recognize revisits.
// It is intended for the interpreter's internal use; hosts should not rely
// on it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
type SliceIdentity struct {
	Ptr uintptr
	Len int
	Cap int
}
