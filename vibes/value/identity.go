package value

// SliceIdentity captures the identity of a slice header so cycle
// detection in value graphs can recognize revisits.
type SliceIdentity struct {
	Ptr uintptr
	Len int
	Cap int
}
