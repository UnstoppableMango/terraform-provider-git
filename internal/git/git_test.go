package git_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

var _ = Describe("Auth", func() {
	It("treats the zero value as unauthenticated", func() {
		var auth git.Auth
		Expect(auth.Token).To(BeEmpty())
	})
})

var _ = Describe("Ref", func() {
	It("treats the zero value as an empty ref", func() {
		var ref git.Ref
		Expect(ref.Name).To(BeEmpty())
		Expect(ref.Hash).To(BeEmpty())
	})
})
