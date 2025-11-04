// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

package engine

import (
	"github.com/sprucehealth/twimulator/model"

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
