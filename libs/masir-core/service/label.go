package service

// EnsureBinaryReady repairs a service label only when the installed inode needs it.
func EnsureBinaryReady(bin string) error {
	if LabelOK(bin) {
		return nil
	}
	return EnsureBinaryLabel(bin)
}
