package utils

import (
	"fmt"
	"net/http"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// buildApiError returns new error with updated status
func buildApiError(err error, msg string) error {
	var buildError error
	switch {
	case apiErrors.IsNotFound(err):
		// Handle "Not Found" error
		buildError = &apiErrors.StatusError{ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusNotFound,
			Message: msg,
			Reason:  metav1.StatusReasonNotFound,
		}}
	case apiErrors.IsForbidden(err):
		// Handle "Forbidden" error
		buildError = &apiErrors.StatusError{ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusForbidden,
			Message: msg,
			Reason:  metav1.StatusReasonForbidden,
		}}
	default:
		// Handle other error types
		buildError = fmt.Errorf("error %w does not match the following http error codes: %d, %d", err, http.StatusNotFound, http.StatusForbidden)
	}
	return buildError
}
