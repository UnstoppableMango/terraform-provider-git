package gogit

// This file covers authMethod's SSH dispatch directly (unexported, so it
// lives in package gogit, not gogit_test) rather than through a live SSH
// server, since ssh.NewPublicKeys/NewPublicKeysFromFile/NewSSHAgentAuth are
// pure key-parsing/local-agent-lookup and don't need a network round trip to
// exercise.

import (
	"os"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

// testPrivateKeyPEM is a throwaway, unencrypted Ed25519 private key used only
// to exercise authMethod's PEM parsing path; it authenticates nothing.
const testPrivateKeyPEM = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACBzbr5qsgWVLeYKfgTpq/F5PejSQqw6xLMtYkW5C0vA0gAAAJjOjKLOzoyi
zgAAAAtzc2gtZWQyNTUxOQAAACBzbr5qsgWVLeYKfgTpq/F5PejSQqw6xLMtYkW5C0vA0g
AAAEBow3Jer5XUozzp170wXBtH8zG/8jzYyxqifIV+XkNInHNuvmqyBZUt5gp+BOmr8Xk9
6NJCrDrEsy1iRbkLS8DSAAAAFXRlc3RAdGVzdC5leGFtcGxlLmNvbQ==
-----END OPENSSH PRIVATE KEY-----`

var _ = Describe("authMethod", func() {
	It("returns nil for an empty Auth", func() {
		method, err := authMethod(providergit.Auth{})
		Expect(err).NotTo(HaveOccurred())
		Expect(method).To(BeNil())
	})

	It("returns http.BasicAuth for a token", func() {
		method, err := authMethod(providergit.Auth{Token: "tok", Host: "github"})
		Expect(err).NotTo(HaveOccurred())

		basic, ok := method.(*http.BasicAuth)
		Expect(ok).To(BeTrue(), "expected *http.BasicAuth")
		Expect(basic.Username).To(Equal("x-access-token"))
		Expect(basic.Password).To(Equal("tok"))
	})

	It("returns an error for invalid SSH private key content", func() {
		_, err := authMethod(providergit.Auth{SSHPrivateKey: "not a real key"})
		Expect(err).To(HaveOccurred())
	})

	It("returns ssh.PublicKeys for valid SSH private key content, defaulting user to git", func() {
		method, err := authMethod(providergit.Auth{SSHPrivateKey: testPrivateKeyPEM})
		Expect(err).NotTo(HaveOccurred())

		keys, ok := method.(*gogitssh.PublicKeys)
		Expect(ok).To(BeTrue(), "expected *ssh.PublicKeys")
		Expect(keys.User).To(Equal("git"))
	})

	It("honors an explicit SSH user", func() {
		method, err := authMethod(providergit.Auth{SSHPrivateKey: testPrivateKeyPEM, SSHUser: "someone"})
		Expect(err).NotTo(HaveOccurred())

		keys, ok := method.(*gogitssh.PublicKeys)
		Expect(ok).To(BeTrue(), "expected *ssh.PublicKeys")
		Expect(keys.User).To(Equal("someone"))
	})

	It("returns an error for a nonexistent private key path", func() {
		_, err := authMethod(providergit.Auth{SSHPrivateKeyPath: "/nonexistent/path/to/key"})
		Expect(err).To(HaveOccurred())
	})

	It("reads SSH private key content from a file for SSHPrivateKeyPath", func() {
		f, err := os.CreateTemp("", "gogit-authmethod-key-*")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = os.Remove(f.Name()) }()
		Expect(os.WriteFile(f.Name(), []byte(testPrivateKeyPEM), 0o600)).To(Succeed())

		method, err := authMethod(providergit.Auth{SSHPrivateKeyPath: f.Name()})
		Expect(err).NotTo(HaveOccurred())

		_, ok := method.(*gogitssh.PublicKeys)
		Expect(ok).To(BeTrue(), "expected *ssh.PublicKeys")
	})

	It("attempts SSH agent auth when SSHAgent is set with no key material", func() {
		// NewSSHAgentAuth only fails if it can't open a pipe to an agent
		// (e.g. SSH_AUTH_SOCK unset/invalid) or determine a username; either
		// way it must not panic, and on success it returns a callback-based
		// auth method rather than a static key.
		method, err := authMethod(providergit.Auth{SSHAgent: true})
		if err != nil {
			Expect(method).To(BeNil())
			return
		}

		_, ok := method.(*gogitssh.PublicKeysCallback)
		Expect(ok).To(BeTrue(), "expected *ssh.PublicKeysCallback")
	})
})
