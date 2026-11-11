package updatebundle

// LegacyWindowsVersion returns empty string because the new public release starts
// from 0.1.0, and pre-0.1.0 legacy installations are not authenticated automatically.
func LegacyWindowsVersion(_ string) string {
	return ""
}

