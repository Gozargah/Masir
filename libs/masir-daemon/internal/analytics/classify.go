package analytics

import coreanalytics "github.com/Gozargah/Masir/libs/masir-core/analytics"

// Classification is shared with offline CLI and GUI reporting.
func ErrorKind(err error) string   { return coreanalytics.ErrorKind(err) }
func ErrorReason(err error) string { return coreanalytics.ErrorReason(err) }
func GRPCCode(err error) string    { return coreanalytics.GRPCCode(err) }
func HTTPStatus(err error) int     { return coreanalytics.HTTPStatus(err) }
func RPCError(err error) error     { return coreanalytics.RPCError(err) }
