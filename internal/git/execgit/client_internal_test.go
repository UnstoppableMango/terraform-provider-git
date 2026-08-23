package execgit

// This file covers isLeaseRejection, which classifies a failed
// `--force-with-lease` push as either a compare-and-swap conflict (the
// branch moved since it was last observed) or a real error. The function is
// unexported, so this lives in package execgit (not execgit_test) to reach
// it directly; its specs run under the TestExecgit entrypoint in
// execgit_suite_test.go.
//
// The distinction matters beyond tidiness: a message classified as a lease
// rejection surfaces to the user as "Conflict Detected: Branch Tip Changed
// Since Last Read", which tells them to re-run apply. An auth or policy
// failure misclassified that way sends them into a loop that can never
// succeed. See DESIGN.md's "Auth revoked/expired between Read and Update".

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pushError wraps msg the way runGit wraps a failed push's stderr, so the
// cases below exercise the same shape isLeaseRejection sees in production.
func pushError(msg string) error {
	return fmt.Errorf("git push origin HEAD:refs/heads/main: %w: %s",
		fmt.Errorf("exit status 1"), msg)
}

var _ = Describe("isLeaseRejection", func() {
	DescribeTable("classifying a failed push",
		func(stderr string, expected bool) {
			Expect(isLeaseRejection(pushError(stderr))).To(Equal(expected))
		},
		// Client-side rejections: git itself refused the update, which for a
		// --force-with-lease push means the lease check failed.
		Entry("a stale-info lease rejection",
			" ! [rejected]        main -> main (stale info)\nerror: failed to push some refs to 'file:///tmp/repo'",
			true),
		Entry("a client-side non-fast-forward rejection",
			" ! [rejected]        main -> main (non-fast-forward)\nerror: failed to push some refs to 'file:///tmp/repo'",
			true),

		// Server-side rejections and transport failures: auth or policy
		// problems that re-running apply cannot resolve.
		Entry("a permission denied rejection from the remote",
			" ! [remote rejected] main -> main (permission denied)\nerror: failed to push some refs to 'https://example.com/repo.git'",
			false),
		Entry("a pre-receive hook declining the push",
			" ! [remote rejected] main -> main (pre-receive hook declined)\nerror: failed to push some refs to 'https://example.com/repo.git'",
			false),
		Entry("a failed authentication",
			"remote: Invalid username or password.\nfatal: Authentication failed for 'https://example.com/repo.git/'",
			false),
		Entry("a 403 from the host",
			"fatal: unable to access 'https://example.com/repo.git/': The requested URL returned error: 403",
			false),
		Entry("an unreachable remote",
			"fatal: unable to access 'https://example.com/repo.git/': Could not resolve host: example.com",
			false),
	)
})
