package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func labelConfig(baseURL, name string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
data "vibeoncall_label" "test" {
  name = %q
}
`, name)
}

func TestLabelDataSourceFindsExactName(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.labels = []fakeLabel{
		{ID: 1, Name: "sev1", Color: "#000000"},
		{ID: 2, Name: "Sev1 Customer", Color: "#111111"},
		{ID: 3, Name: "Sev1", Color: "#FF0000"},
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: labelConfig(srv.URL, "Sev1"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.vibeoncall_label.test", "id", "3"),
				resource.TestCheckResourceAttr("data.vibeoncall_label.test", "color", "#FF0000"),
			),
		}},
	})
}

func TestLabelDataSourceErrorsWhenNoLabelMatches(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.labels = []fakeLabel{{ID: 1, Name: "Sev1 Customer", Color: "#111111"}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      labelConfig(srv.URL, "Sev1"),
			ExpectError: regexp.MustCompile(`Label not found`),
		}},
	})
}
