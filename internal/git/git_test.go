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

var _ = Describe("NormalizeURL", func() {
	DescribeTable("rewrites SSH URLs to https",
		func(url, expected string) {
			normalized, changed := git.NormalizeURL(url)

			Expect(normalized).To(Equal(expected))
			Expect(changed).To(BeTrue())
		},
		Entry("scp-like", "git@github.com:owner/repo.git", "https://github.com/owner/repo.git"),
		Entry("scp-like without a user", "github.com:owner/repo.git", "https://github.com/owner/repo.git"),
		Entry("scp-like with a nested path", "git@gitlab.com:group/sub/repo.git", "https://gitlab.com/group/sub/repo.git"),
		Entry("ssh scheme", "ssh://git@github.com/owner/repo.git", "https://github.com/owner/repo.git"),
		Entry("ssh scheme with a port", "ssh://git@github.com:22/owner/repo.git", "https://github.com/owner/repo.git"),
	)

	DescribeTable("leaves everything else alone",
		func(url string) {
			normalized, changed := git.NormalizeURL(url)

			Expect(normalized).To(Equal(url))
			Expect(changed).To(BeFalse())
		},
		Entry("https", "https://github.com/owner/repo.git"),
		Entry("http", "http://example.com/repo.git"),
		Entry("git protocol", "git://example.com/repo.git"),
		Entry("file url", "file:///srv/git/repo.git"),
		Entry("absolute path", "/srv/git/repo.git"),
		Entry("relative path", "../repo.git"),
		Entry("windows-style path", "C:/src/repo"),
	)
})

var _ = Describe("HostFromURL", func() {
	DescribeTable("maps a URL's hostname to a host type",
		func(url, expected string) {
			Expect(git.HostFromURL(url)).To(Equal(expected))
		},
		Entry("github https", "https://github.com/owner/repo.git", "github"),
		Entry("github ssh", "git@github.com:owner/repo.git", "github"),
		Entry("self-hosted github", "https://github.example.com/owner/repo.git", "github"),
		Entry("gitlab https", "https://gitlab.com/group/repo.git", "gitlab"),
		Entry("gitlab ssh", "git@gitlab.com:group/repo.git", "gitlab"),
		Entry("self-hosted gitlab", "https://gitlab.example.com/group/repo.git", "gitlab"),
		Entry("uppercase hostname", "https://GitHub.com/owner/repo.git", "github"),
		Entry("with credentials", "https://user@github.com/owner/repo.git", "github"),
		Entry("unknown host", "https://git.example.com/owner/repo.git", "generic"),
		Entry("local path", "/srv/git/repo.git", "generic"),
	)
})

var _ = Describe("Ref", func() {
	It("treats the zero value as an empty ref", func() {
		var ref git.Ref
		Expect(ref.Name).To(BeEmpty())
		Expect(ref.Hash).To(BeEmpty())
	})
})
