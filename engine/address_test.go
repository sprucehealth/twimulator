// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

package engine_test

import (
	"testing"

	twilioopenapi "github.com/twilio/twilio-go/rest/api/v2010"

	"github.com/sprucehealth/twimulator/engine"
	"github.com/sprucehealth/twimulator/twilioapi"
)

func TestCreateAddress(t *testing.T) {
	// Create engine with manual clock
	eng := engine.NewEngine(engine.WithManualClock())
	defer eng.Close()

	// Create a subaccount
	accountParams := &twilioopenapi.CreateAccountParams{}
	account, err := eng.CreateAccount(accountParams)
	if err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}

	// Create client for the subaccount
	client := twilioapi.NewClient(*account.Sid, eng)

	// Test creating an address with all required fields
	t.Run("CreateAddressWithRequiredFields", func(t *testing.T) {
		customerName := "John Doe"
		street := "123 Main St"
		city := "San Francisco"
		region := "CA"
		postalCode := "94102"
		isoCountry := "US"

		params := &twilioopenapi.CreateAddressParams{}
		params.SetCustomerName(customerName)
		params.SetStreet(street)
		params.SetCity(city)
		params.SetRegion(region)
		params.SetPostalCode(postalCode)
		params.SetIsoCountry(isoCountry)

		address, err := client.CreateAddress(params)
		if err != nil {
			t.Fatalf("Failed to create address: %v", err)
		}

		if address.Sid == nil || *address.Sid == "" {
			t.Error("Expected address SID to be set")
		}
		if address.AccountSid == nil || *address.AccountSid != *account.Sid {
			t.Errorf("Expected account SID %s, got %v", *account.Sid, address.AccountSid)
		}
		if address.CustomerName == nil || *address.CustomerName != customerName {
			t.Errorf("Expected customer name %s, got %v", customerName, address.CustomerName)
		}
		if address.Street == nil || *address.Street != street {
			t.Errorf("Expected street %s, got %v", street, address.Street)
		}
		if address.City == nil || *address.City != city {
			t.Errorf("Expected city %s, got %v", city, address.City)
		}
		if address.Region == nil || *address.Region != region {
			t.Errorf("Expected region %s, got %v", region, address.Region)
		}
		if address.PostalCode == nil || *address.PostalCode != postalCode {
			t.Errorf("Expected postal code %s, got %v", postalCode, address.PostalCode)
		}
		if address.IsoCountry == nil || *address.IsoCountry != isoCountry {
			t.Errorf("Expected ISO country %s, got %v", isoCountry, address.IsoCountry)
		}
		if address.DateCreated == nil || *address.DateCreated == "" {
			t.Error("Expected date created to be set")
		}
		if address.DateUpdated == nil || *address.DateUpdated == "" {
			t.Error("Expected date updated to be set")
		}
	})

	// Test creating an address with optional fields
	t.Run("CreateAddressWithOptionalFields", func(t *testing.T) {
		customerName := "Jane Smith"
		street := "456 Oak Ave"
		streetSecondary := "Apt 2B"
		city := "Los Angeles"
		region := "CA"
		postalCode := "90001"
		isoCountry := "US"
		friendlyName := "Home Address"
		emergencyEnabled := true

		params := &twilioopenapi.CreateAddressParams{}
		params.SetCustomerName(customerName)
		params.SetStreet(street)
		params.SetStreetSecondary(streetSecondary)
		params.SetCity(city)
		params.SetRegion(region)
		params.SetPostalCode(postalCode)
		params.SetIsoCountry(isoCountry)
		params.SetFriendlyName(friendlyName)
		params.SetEmergencyEnabled(emergencyEnabled)

		address, err := client.CreateAddress(params)
		if err != nil {
			t.Fatalf("Failed to create address: %v", err)
		}

		if address.StreetSecondary == nil || *address.StreetSecondary != streetSecondary {
			t.Errorf("Expected street secondary %s, got %v", streetSecondary, address.StreetSecondary)
		}
		if address.FriendlyName == nil || *address.FriendlyName != friendlyName {
			t.Errorf("Expected friendly name %s, got %v", friendlyName, address.FriendlyName)
		}
		if address.EmergencyEnabled == nil || *address.EmergencyEnabled != emergencyEnabled {
			t.Errorf("Expected emergency enabled %v, got %v", emergencyEnabled, address.EmergencyEnabled)
		}
	})

	// Test creating an address with missing required fields
	t.Run("CreateAddressWithMissingRequiredFields", func(t *testing.T) {
		testCases := []struct {
			name   string
			params *twilioopenapi.CreateAddressParams
		}{
			{
				name: "MissingCustomerName",
				params: func() *twilioopenapi.CreateAddressParams {
					p := &twilioopenapi.CreateAddressParams{}
					p.SetStreet("123 Main St")
					p.SetCity("San Francisco")
					p.SetRegion("CA")
					p.SetPostalCode("94102")
					p.SetIsoCountry("US")
					return p
				}(),
			},
			{
				name: "MissingStreet",
				params: func() *twilioopenapi.CreateAddressParams {
					p := &twilioopenapi.CreateAddressParams{}
					p.SetCustomerName("John Doe")
					p.SetCity("San Francisco")
					p.SetRegion("CA")
					p.SetPostalCode("94102")
					p.SetIsoCountry("US")
					return p
				}(),
			},
			{
				name: "MissingCity",
				params: func() *twilioopenapi.CreateAddressParams {
					p := &twilioopenapi.CreateAddressParams{}
					p.SetCustomerName("John Doe")
					p.SetStreet("123 Main St")
					p.SetRegion("CA")
					p.SetPostalCode("94102")
					p.SetIsoCountry("US")
					return p
				}(),
			},
			{
				name: "MissingRegion",
				params: func() *twilioopenapi.CreateAddressParams {
					p := &twilioopenapi.CreateAddressParams{}
					p.SetCustomerName("John Doe")
					p.SetStreet("123 Main St")
					p.SetCity("San Francisco")
					p.SetPostalCode("94102")
					p.SetIsoCountry("US")
					return p
				}(),
			},
			{
				name: "MissingPostalCode",
				params: func() *twilioopenapi.CreateAddressParams {
					p := &twilioopenapi.CreateAddressParams{}
					p.SetCustomerName("John Doe")
					p.SetStreet("123 Main St")
					p.SetCity("San Francisco")
					p.SetRegion("CA")
					p.SetIsoCountry("US")
					return p
				}(),
			},
			{
				name: "MissingIsoCountry",
				params: func() *twilioopenapi.CreateAddressParams {
					p := &twilioopenapi.CreateAddressParams{}
					p.SetCustomerName("John Doe")
					p.SetStreet("123 Main St")
					p.SetCity("San Francisco")
					p.SetRegion("CA")
					p.SetPostalCode("94102")
					return p
				}(),
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := client.CreateAddress(tc.params)
				if err == nil {
					t.Errorf("Expected error for %s, but got none", tc.name)
				}
			})
		}
	})

	// Test AutoCorrectAddress flag
	t.Run("AutoCorrectAddressFlag", func(t *testing.T) {
		customerName := "Test User"
		street := "789 Pine Rd"
		city := "Seattle"
		region := "WA"
		postalCode := "98101"
		isoCountry := "US"
		autoCorrect := false

		params := &twilioopenapi.CreateAddressParams{}
		params.SetCustomerName(customerName)
		params.SetStreet(street)
		params.SetCity(city)
		params.SetRegion(region)
		params.SetPostalCode(postalCode)
		params.SetIsoCountry(isoCountry)
		params.SetAutoCorrectAddress(autoCorrect)

		address, err := client.CreateAddress(params)
		if err != nil {
			t.Fatalf("Failed to create address: %v", err)
		}

		if address.Validated == nil || *address.Validated != autoCorrect {
			t.Errorf("Expected validated %v, got %v", autoCorrect, address.Validated)
		}
	})
}
