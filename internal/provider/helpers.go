package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"

	"github.com/vibraniumlabs/terraform-provider-vibeoncall/internal/client"
)

const listPageSize = 100

var numericIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

var numericID = stringvalidator.RegexMatches(numericIDPattern, "must be a numeric ID")

var int32NumericID validator.String = int32IDValidator{}

type int32IDValidator struct{}

func (int32IDValidator) Description(context.Context) string {
	return "must be a numeric ID of at most 2147483647"
}

func (v int32IDValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v int32IDValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if _, err := strconv.ParseInt(s, 10, 32); err != nil || !isNumericID(s) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid ID", fmt.Sprintf("%q %s.", s, v.Description(ctx)))
	}
}

// Matches the characters JavaScript's String.prototype.trim removes, which the API applies to names, descriptions and searches.
var noSurroundingWhitespace = stringvalidator.RegexMatches(
	regexp.MustCompile(`(?s)^[^\s\x0B\p{Z}\x{FEFF}](.*[^\s\x0B\p{Z}\x{FEFF}])?$`),
	"must not be empty or start or end with whitespace",
)

func isNumericID(s string) bool {
	return numericIDPattern.MatchString(s)
}

func int32ID(v types.String, attr string, diags *diag.Diagnostics) (int32, bool) {
	id, err := strconv.ParseInt(v.ValueString(), 10, 32)
	if err != nil {
		diags.AddError("Invalid "+attr, err.Error())
		return 0, false
	}
	return int32(id), true
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
