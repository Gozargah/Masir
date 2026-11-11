package updatebundle

// LegacyDarwinVersion returns empty string because the new public release starts
// from 0.1.0, and pre-0.1.0 legacy installations are not authenticated automatically.
func LegacyDarwinVersion(_ string) string {
	return ""
}

