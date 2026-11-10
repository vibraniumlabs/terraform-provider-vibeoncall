package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func memberConfig(baseURL, email string) string {
	return providerConfig(baseURL) + fmt.Sprintf(`
data "vibeoncall_member" "test" {
  email = %q
}
`, email)
}

func TestMemberDataSourceFindsExactEmailAcrossPages(t *testing.T) {
	users := []fakeUser{}
	for i := 1; i <= 150; i++ {
		users = append(users, fakeUser{ID: i, Email: fmt.Sprintf("%03d-a@x.io", i), Status: "active"})
	}
	users = append(users, fakeUser{ID: 999, Email: "a@x.io", FullName: strPtr("Ada"), Status: "pending"})
	_, srv := newFakeAPI(t, users...)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: memberConfig(srv.URL, "A@X.io"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.vibeoncall_member.test", "id", "999"),
				resource.TestCheckResourceAttr("data.vibeoncall_member.test", "email", "A@X.io"),
				resource.TestCheckResourceAttr("data.vibeoncall_member.test", "full_name", "Ada"),
				resource.TestCheckResourceAttr("data.vibeoncall_member.test", "status", "pending"),
			),
		}},
	})
}

func TestMemberDataSourceErrorsWhenNotExactlyOneMatch(t *testing.T) {
	_, srv := newFakeAPI(t,
		fakeUser{ID: 1, Email: "ada@example.com.au", Status: "active"},
		fakeUser{ID: 2, Email: "grace@example.com", Status: "active"},
		fakeUser{ID: 3, Email: "Grace@Example.com", Status: "revoked"},
	)

	for email, want := range map[string]string{
		"ada@example.com":   `Member not found`,
		"grace@example.com": `Multiple members found`,
	} {
		t.Run(email, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      memberConfig(srv.URL, email),
					ExpectError: regexp.MustCompile(want),
				}},
			})
		})
	}
}
