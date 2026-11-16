package analytics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorKind names a failure in a small, stable vocabulary, so dashboards don't have to
// parse messages.
func ErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var tagged interface{ DiagnosticReason() string }
	if errors.As(err, &tagged) && tagged.DiagnosticReason() != "" {
		if k := diagnosticKind(tagged.DiagnosticReason()); k != "" {
			return k
		}
	}
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == "masir" {
			if k := info.Metadata["error_kind"]; k != "" {
				return k
			}
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var certErr *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	if errors.As(err, &certErr) || errors.As(err, &unknownCA) {
		return "tls"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	if st, ok := status.FromError(err); ok && st.Code().String() != "OK" {
		if kind := grpcKind(st.Code().String()); kind != "" {
			return kind
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission denied") || strings.Contains(msg, "operation not permitted"):
		return "permission_denied"
	case strings.Contains(msg, "address already in use"):
		return "port_in_use"
	case strings.Contains(msg, "failed to add system route"):
		return "tun_route_conflict"
	case strings.Contains(msg, "wintun"):
		return "tun_driver"
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "name resolution"):
		return "dns"
	case strings.Contains(msg, "connection refused"):
		return "connection_refused"
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "timed out") || strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "tls") || strings.Contains(msg, "certificate"):
		return "tls"
	case strings.Contains(msg, "checksum"):
		return "checksum"
	case strings.Contains(msg, "read-only file system"):
		return "read_only"
	case strings.Contains(msg, "no space left"):
		return "disk_full"
	case strings.Contains(msg, "text file busy") || strings.Contains(msg, "being used by another process"):
		return "file_busy"
	case strings.Contains(msg, "not found"):
		return "not_found"
	case strings.Contains(msg, "already exists"):
		return "already_exists"
	case strings.Contains(msg, "invalid"):
		return "invalid"
	case strings.Contains(msg, "fetch") || strings.Contains(msg, "subscription"):
		return "subscription"
	case strings.Contains(msg, "config") || strings.Contains(msg, "xray"):
		return "xray_config"
	}
	return "other"
}

func grpcKind(code string) string {
	switch code {
	case "InvalidArgument":
		return "invalid"
	case "NotFound":
		return "not_found"
	case "AlreadyExists":
		return "already_exists"
	case "PermissionDenied":
		return "permission_denied"
	case "Unauthenticated":
		return "unauthenticated"
	case "FailedPrecondition":
		return "precondition"
	case "Unavailable":
		return "unavailable"
	case "DeadlineExceeded":
		return "timeout"
	case "Canceled":
		return "cancelled"
	case "ResourceExhausted":
		return "rate_limited"
	case "Internal":
		return "internal"
	}
	return ""
}

// GRPCCode is the status code name, or empty for non-gRPC errors.
func GRPCCode(err error) string {
	if err == nil {
		return ""
	}
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	if st.Code().String() == "OK" {
		return ""
	}
	return st.Code().String()
}

// ErrorReason is the masir ErrorInfo reason, e.g. ROOT_REQUIRED.
func ErrorReason(err error) string {
	if err == nil {
		return ""
	}
	var tagged interface{ DiagnosticReason() string }
	if errors.As(err, &tagged) {
		return tagged.DiagnosticReason()
	}
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == "masir" {
			return info.Reason
		}
	}
	return ""
}

func diagnosticKind(reason string) string {
	switch reason {
	case "release_http_status":
		return "http"
	case "release_rate_limited":
		return "rate_limited"
	case "release_not_found":
		return "not_found"
	case "release_invalid_response":
		return "invalid"
	}
	return ""
}

// HTTPStatus is structured diagnostic context, never parsed from private messages.
func HTTPStatus(err error) int {
	var tagged interface{ DiagnosticHTTPStatus() int }
	if errors.As(err, &tagged) {
		return tagged.DiagnosticHTTPStatus()
	}
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == "masir" {
			n, _ := strconv.Atoi(info.Metadata["http_status"])
			return n
		}
	}
	return 0
}

// RPCError preserves safe diagnostics at the RPC boundary without changing the
// user-facing message or status code.
func RPCError(err error) error {
	if err == nil {
		return nil
	}
	reason := ErrorReason(err)
	if reason == "" {
		reason = "release_" + ErrorKind(err)
	}
	meta := map[string]string{"error_kind": ErrorKind(err)}
	if code := HTTPStatus(err); code != 0 {
		meta["http_status"] = strconv.Itoa(code)
	}
	st, e := status.New(codes.Unknown, err.Error()).WithDetails(&errdetails.ErrorInfo{Domain: "masir", Reason: reason, Metadata: meta})
	if e != nil {
		return status.Error(codes.Unknown, err.Error())
	}
	return st.Err()
}
