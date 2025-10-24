package engine

import (
	"twimulator/model"

	"github.com/twilio/twilio-go/client"
)

const ErrorCodeResourceNotFound = 20404

func notFoundError(sid model.SID) *client.TwilioRestError {
	return &client.TwilioRestError{
		Code:     ErrorCodeResourceNotFound,
		Details:  nil,
		Message:  "Resource not found: " + sid.String() + " ",
		MoreInfo: "",
		Status:   ErrorCodeResourceNotFound,
	}
}
