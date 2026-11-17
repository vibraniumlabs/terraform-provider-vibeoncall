package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf16"

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

// The API checks lengths with JavaScript's String length, which counts UTF-16 code units: an emoji counts as two.
func utf16LengthAtMost(limit int) validator.String {
	return utf16LengthValidator{max: limit}
}

type utf16LengthValidator struct{ max int }

func (v utf16LengthValidator) Description(context.Context) string {
	return fmt.Sprintf("must be at most %d UTF-16 code units long, where characters such as emoji count as two", v.max)
}

func (v utf16LengthValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v utf16LengthValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if n := len(utf16.Encode([]rune(req.ConfigValue.ValueString()))); n > v.max {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Attribute Value Length", fmt.Sprintf("Attribute %s %s, got: %d", req.Path, v.Description(ctx), n))
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

func nullableID[T int | int32](v types.String) nullable.Nullable[T] {
	if v.IsNull() {
		return nullable.NewNullNullable[T]()
	}
	return nullable.NewNullableWithValue(T(mustAtoi(v)))
}

func idFromNullable[T int | int32](v nullable.Nullable[T]) types.String {
	id, err := v.Get()
	if err != nil {
		return types.StringNull()
	}
	return itoa(int(id))
}

func mustAtoi(v types.String) int {
	n, _ := strconv.ParseInt(v.ValueString(), 10, 32)
	return int(n)
}

func itoa(n int) types.String {
	return types.StringValue(strconv.Itoa(n))
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

const millisecondsPerSecond = 1_000

func secondsFromMs[T int | int64](ms T) int64 {
	return int64((ms + millisecondsPerSecond/2) / millisecondsPerSecond)
}
