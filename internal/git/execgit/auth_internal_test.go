package execgit

// This file covers gitEnv's SSH branch directly (unexported, so it lives in
// package execgit, not execgit_test).

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			return v, true
		}
	}
	return "", false
}

var _ = Describe("gitEnv", func() {
	It("sets no GIT_SSH_COMMAND for an empty Auth", func() {
		// Deliberately doesn't assert on GIT_ASKPASS here: some development
		// environments (e.g. VS Code) already export their own GIT_ASKPASS
		// ambiently, which gitEnv passes through unchanged via os.Environ()
		// when auth.Token is unset. That's expected passthrough, not
		// something gitEnv itself sets.
		env, cleanup, err := gitEnv(providergit.Auth{})
		defer cleanup()

		Expect(err).NotTo(HaveOccurred())
		_, hasSSHCommand := envValue(env, "GIT_SSH_COMMAND")
		Expect(hasSSHCommand).To(BeFalse())
	})

	It("writes a temp key file and wrapper script for inline SSHPrivateKey content", func() {
		env, cleanup, err := gitEnv(providergit.Auth{SSHPrivateKey: "fake-pem-content"})
		Expect(err).NotTo(HaveOccurred())
		defer cleanup()

		keyPath, ok := envValue(env, "GIT_PROVIDER_SSH_KEY")
		Expect(ok).To(BeTrue())
		wrapperPath, ok := envValue(env, "GIT_SSH_COMMAND")
		Expect(ok).To(BeTrue())

		content, err := os.ReadFile(keyPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal("fake-pem-content"))

		info, err := os.Stat(keyPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))

		Expect(wrapperPath).To(BeAnExistingFile())

		cleanup()
		_, err = os.Stat(keyPath)
		Expect(os.IsNotExist(err)).To(BeTrue(), "expected key file to be removed by cleanup")
		_, err = os.Stat(wrapperPath)
		Expect(os.IsNotExist(err)).To(BeTrue(), "expected wrapper script to be removed by cleanup")
	})

	It("uses SSHPrivateKeyPath directly without writing a temp key file", func() {
		f, err := os.CreateTemp("", "execgit-gitenv-key-*")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = os.Remove(f.Name()) }()
		Expect(f.Close()).To(Succeed())

		env, cleanup, err := gitEnv(providergit.Auth{SSHPrivateKeyPath: f.Name()})
		Expect(err).NotTo(HaveOccurred())
		defer cleanup()

		keyPath, ok := envValue(env, "GIT_PROVIDER_SSH_KEY")
		Expect(ok).To(BeTrue())
		Expect(keyPath).To(Equal(f.Name()))
	})

	It("errors on a passphrase-protected key without touching the filesystem", func() {
		_, _, err := gitEnv(providergit.Auth{SSHPrivateKey: "fake-pem-content", SSHPassphrase: "secret"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("passphrase"))
	})

	It("errors on a passphrase-protected key path too", func() {
		_, _, err := gitEnv(providergit.Auth{SSHPrivateKeyPath: "/some/path", SSHPassphrase: "secret"})
		Expect(err).To(HaveOccurred())
	})

	It("sets no SSH env when SSHAgent is set with no key material, relying on the ambient environment", func() {
		env, cleanup, err := gitEnv(providergit.Auth{SSHAgent: true})
		defer cleanup()

		Expect(err).NotTo(HaveOccurred())
		_, hasSSHCommand := envValue(env, "GIT_SSH_COMMAND")
		Expect(hasSSHCommand).To(BeFalse())
	})

	It("still prefers the askpass/token path when only Token is set", func() {
		env, cleanup, err := gitEnv(providergit.Auth{Token: "tok", Host: "gitlab"})
		Expect(err).NotTo(HaveOccurred())
		defer cleanup()

		_, hasSSHCommand := envValue(env, "GIT_SSH_COMMAND")
		Expect(hasSSHCommand).To(BeFalse())
		_, hasAskpass := envValue(env, "GIT_ASKPASS")
		Expect(hasAskpass).To(BeTrue())
	})
})
