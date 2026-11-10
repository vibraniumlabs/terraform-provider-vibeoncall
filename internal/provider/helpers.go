package provider

import (
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

var numericIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

var numericID = stringvalidator.RegexMatches(numericIDPattern, "must be a numeric ID")

func isNumericID(s string) bool {
	return numericIDPattern.MatchString(s)
}

func clientFromProviderData(providerData any, diags *diag.Diagnostics) *client.ClientWithResponses {
	if providerData == nil {
		return nil
	}
	c, ok := providerData.(*client.ClientWithResponses)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("got %T", providerData))
	}
	return c
}

func nullableString(v types.String) nullable.Nullable[string] {
	if v.IsNull() {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(v.ValueString())
}

func stringFromNullable(v nullable.Nullable[string]) types.String {
	s, err := v.Get()
	if err != nil {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func apiError(status int, body []byte) string {
	return fmt.Sprintf("HTTP %d: %s", status, string(body))
}
