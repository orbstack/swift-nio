package dockerclient

import "errors"

func IsStatusError(err error, status int) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus == status {
		return true
	}
	return false
}
