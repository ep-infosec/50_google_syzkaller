// Copyright 2017 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/syzkaller/dashboard/dashapi"
	"github.com/google/syzkaller/pkg/email"
	"github.com/google/syzkaller/sys/targets"
)

// nolint: funlen
func TestEmailReport(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	crash := testCrash(build, 1)
	crash.Maintainers = []string{`"Foo Bar" <foo@bar.com>`, `bar@foo.com`, `idont@want.EMAILS`}
	c.client2.ReportCrash(crash)

	// Report the crash over email and check all fields.
	var sender0, extBugID0, body0 string
	var dbBug0 *Bug
	{
		msg := c.pollEmailBug()
		sender0 = msg.Sender
		body0 = msg.Body
		sender, extBugID, err := email.RemoveAddrContext(msg.Sender)
		c.expectOK(err)
		extBugID0 = extBugID
		dbBug, dbCrash, dbBuild := c.loadBug(extBugID0)
		dbBug0 = dbBug
		crashLogLink := externalLink(c.ctx, textCrashLog, dbCrash.Log)
		kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
		c.expectEQ(sender, fromAddr(c.ctx))
		to := config.Namespaces["test2"].Reporting[0].Config.(*EmailConfig).Email
		c.expectEQ(msg.To, []string{to})
		c.expectEQ(msg.Subject, crash.Title)
		c.expectEQ(len(msg.Attachments), 0)
		c.expectEQ(msg.Body, fmt.Sprintf(`Hello,

syzbot found the following issue on:

HEAD commit:    111111111111 kernel_commit_title1
git tree:       repo1 branch1
console output: %[2]v
kernel config:  %[3]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler1
CC:             [bar@foo.com foo@bar.com idont@want.EMAILS]

Unfortunately, I don't have any reproducer for this issue yet.

IMPORTANT: if you fix the issue, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com

report1

---
This report is generated by a bot. It may contain errors.
See https://goo.gl/tpsmEJ for more information about syzbot.
syzbot engineers can be reached at syzkaller@googlegroups.com.

syzbot will keep track of this issue. See:
https://goo.gl/tpsmEJ#status for how to communicate with syzbot.`,
			extBugID0, crashLogLink, kernelConfigLink))
		c.checkURLContents(crashLogLink, crash.Log)
		c.checkURLContents(kernelConfigLink, build.KernelConfig)
	}

	// Emulate receive of the report from a mailing list.
	// This should update the bug with the link/Message-ID.
	// nolint: lll
	incoming1 := fmt.Sprintf(`Sender: syzkaller@googlegroups.com
Date: Tue, 15 Aug 2017 14:59:00 -0700
Message-ID: <1234>
Subject: crash1
From: %v
To: foo@bar.com
Content-Type: text/plain

Hello

syzbot will keep track of this issue.
If you forgot to add the Reported-by tag, once the fix for this bug is merged
into any tree, please reply to this email with:
#syz fix: exact-commit-title
To mark this as a duplicate of another syzbot report, please reply with:
#syz dup: exact-subject-of-another-report
If it's a one-off invalid bug report, please reply with:
#syz invalid

-- 
You received this message because you are subscribed to the Google Groups "syzkaller" group.
To unsubscribe from this group and stop receiving emails from it, send an email to syzkaller+unsubscribe@googlegroups.com.
To post to this group, send email to syzkaller@googlegroups.com.
To view this discussion on the web visit https://groups.google.com/d/msgid/syzkaller/1234@google.com.
For more options, visit https://groups.google.com/d/optout.
`, sender0)

	_, err := c.POST("/_ah/mail/", incoming1)
	c.expectOK(err)

	// Emulate that somebody sends us our own email back without quoting.
	// We used to extract "#syz fix: exact-commit-title" from it.
	c.incomingEmail(sender0, body0)

	c.incomingEmail(sender0, "I don't want emails", EmailOptFrom(`"idont" <idont@WANT.emails>`))
	c.expectNoEmail()

	// This person sends an email and is listed as a maintainer, but opt-out of emails.
	// We should not send anything else to them for this bug. Also don't warn about no mailing list in CC.
	c.incomingEmail(sender0, "#syz uncc", EmailOptFrom(`"IDONT" <Idont@want.emails>`), EmailOptCC(nil))
	c.expectNoEmail()

	// Now report syz reproducer and check updated email.
	build2 := testBuild(10)
	build2.Arch = targets.I386
	build2.KernelRepo = testConfig.Namespaces["test2"].Repos[0].URL
	build2.KernelBranch = testConfig.Namespaces["test2"].Repos[0].Branch
	build2.KernelCommitTitle = "a really long title, longer than 80 chars, really long-long-long-long-long-long title"
	c.client2.UploadBuild(build2)
	crash.BuildID = build2.ID
	crash.ReproOpts = []byte("repro opts")
	crash.ReproSyz = []byte("getpid()")
	syzRepro := []byte(fmt.Sprintf("# https://testapp.appspot.com/bug?id=%v\n%s#%s\n%s",
		dbBug0.keyHash(), syzReproPrefix, crash.ReproOpts, crash.ReproSyz))
	c.client2.ReportCrash(crash)

	{
		msg := c.pollEmailBug()
		c.expectEQ(msg.Sender, sender0)
		sender, _, err := email.RemoveAddrContext(msg.Sender)
		c.expectOK(err)
		_, dbCrash, dbBuild := c.loadBug(extBugID0)
		reproSyzLink := externalLink(c.ctx, textReproSyz, dbCrash.ReproSyz)
		crashLogLink := externalLink(c.ctx, textCrashLog, dbCrash.Log)
		kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
		c.expectEQ(sender, fromAddr(c.ctx))
		to := []string{
			"always@cc.me",
			"bugs2@syzkaller.com",
			"bugs@syzkaller.com", // This is from incomingEmail.
			"default@sender.com", // This is from incomingEmail.
			"foo@bar.com",
			config.Namespaces["test2"].Reporting[0].Config.(*EmailConfig).Email,
		}
		c.expectEQ(msg.To, to)
		c.expectEQ(msg.Subject, "Re: "+crash.Title)
		c.expectEQ(len(msg.Attachments), 0)
		c.expectEQ(msg.Headers["In-Reply-To"], []string{"<1234>"})
		c.expectEQ(msg.Body, fmt.Sprintf(`syzbot has found a reproducer for the following issue on:

HEAD commit:    101010101010 a really long title, longer than 80 chars, re..
git tree:       repo10alias
console output: %[3]v
kernel config:  %[4]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler10
userspace arch: i386
syz repro:      %[2]v
CC:             [bar@foo.com foo@bar.com maintainers@repo10.org bugs@repo10.org]

IMPORTANT: if you fix the issue, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com

report1
`, extBugID0, reproSyzLink, crashLogLink, kernelConfigLink))
		c.checkURLContents(reproSyzLink, syzRepro)
		c.checkURLContents(crashLogLink, crash.Log)
		c.checkURLContents(kernelConfigLink, build2.KernelConfig)
	}

	// Now upstream the bug and check that it reaches the next reporting.
	c.incomingEmail(sender0, "#syz upstream")

	sender1, extBugID1 := "", ""
	{
		msg := c.pollEmailBug()
		sender1 = msg.Sender
		c.expectNE(sender1, sender0)
		sender, extBugID, err := email.RemoveAddrContext(msg.Sender)
		c.expectOK(err)
		extBugID1 = extBugID
		_, dbCrash, dbBuild := c.loadBug(extBugID1)
		reproSyzLink := externalLink(c.ctx, textReproSyz, dbCrash.ReproSyz)
		crashLogLink := externalLink(c.ctx, textCrashLog, dbCrash.Log)
		kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
		c.expectEQ(sender, fromAddr(c.ctx))
		c.expectEQ(msg.To, []string{
			"always@cc.me",
			"bar@foo.com",
			"bugs@repo10.org",
			"bugs@syzkaller.com",
			"default@maintainers.com",
			"foo@bar.com",
			"maintainers@repo10.org",
		})
		c.expectEQ(msg.Subject, "[syzbot] "+crash.Title)
		c.expectEQ(len(msg.Attachments), 0)
		c.expectEQ(msg.Body, fmt.Sprintf(`Hello,

syzbot found the following issue on:

HEAD commit:    101010101010 a really long title, longer than 80 chars, re..
git tree:       repo10alias
console output: %[3]v
kernel config:  %[4]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler10
userspace arch: i386
syz repro:      %[2]v
CC:             [bar@foo.com foo@bar.com maintainers@repo10.org bugs@repo10.org]

IMPORTANT: if you fix the issue, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com

report1

---
This report is generated by a bot. It may contain errors.
See https://goo.gl/tpsmEJ for more information about syzbot.
syzbot engineers can be reached at syzkaller@googlegroups.com.

syzbot will keep track of this issue. See:
https://goo.gl/tpsmEJ#status for how to communicate with syzbot.
syzbot can test patches for this issue, for details see:
https://goo.gl/tpsmEJ#testing-patches`,
			extBugID1, reproSyzLink, crashLogLink, kernelConfigLink))
		c.checkURLContents(reproSyzLink, syzRepro)
		c.checkURLContents(crashLogLink, crash.Log)
		c.checkURLContents(kernelConfigLink, build2.KernelConfig)
	}

	// Model that somebody adds more emails to CC list.
	incoming3 := fmt.Sprintf(`Sender: syzkaller@googlegroups.com
Date: Tue, 15 Aug 2017 14:59:00 -0700
Message-ID: <1234>
Subject: crash1
From: foo@bar.com
To: %v
CC: new@new.com, "another" <another@another.com>, bar@foo.com, bugs@syzkaller.com, foo@bar.com
Content-Type: text/plain

+more people
`, sender1)

	_, err = c.POST("/_ah/mail/", incoming3)
	c.expectOK(err)

	// Now upload a C reproducer.
	crash.ReproC = []byte("int main() {}")
	crash.Maintainers = []string{"\"qux\" <qux@qux.com>"}
	c.client2.ReportCrash(crash)
	cRepro := []byte(fmt.Sprintf("// https://testapp.appspot.com/bug?id=%v\n%s",
		dbBug0.keyHash(), crash.ReproC))

	{
		msg := c.pollEmailBug()
		c.expectEQ(msg.Sender, sender1)
		sender, _, err := email.RemoveAddrContext(msg.Sender)
		c.expectOK(err)
		_, dbCrash, dbBuild := c.loadBug(extBugID1)
		reproCLink := externalLink(c.ctx, textReproC, dbCrash.ReproC)
		reproSyzLink := externalLink(c.ctx, textReproSyz, dbCrash.ReproSyz)
		crashLogLink := externalLink(c.ctx, textCrashLog, dbCrash.Log)
		kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
		c.expectEQ(sender, fromAddr(c.ctx))
		c.expectEQ(msg.To, []string{
			"always@cc.me",
			"another@another.com", "bar@foo.com", "bugs@repo10.org",
			"bugs@syzkaller.com", "default@maintainers.com", "foo@bar.com",
			"maintainers@repo10.org", "new@new.com", "qux@qux.com"})
		c.expectEQ(msg.Subject, "Re: [syzbot] "+crash.Title)
		c.expectEQ(len(msg.Attachments), 0)
		c.expectEQ(msg.Body, fmt.Sprintf(`syzbot has found a reproducer for the following issue on:

HEAD commit:    101010101010 a really long title, longer than 80 chars, re..
git tree:       repo10alias
console output: %[4]v
kernel config:  %[5]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler10
userspace arch: i386
syz repro:      %[3]v
C reproducer:   %[2]v
CC:             [qux@qux.com maintainers@repo10.org bugs@repo10.org]

IMPORTANT: if you fix the issue, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com

report1
`, extBugID1, reproCLink, reproSyzLink, crashLogLink, kernelConfigLink))
		c.checkURLContents(reproCLink, cRepro)
		c.checkURLContents(reproSyzLink, syzRepro)
		c.checkURLContents(crashLogLink, crash.Log)
		c.checkURLContents(kernelConfigLink, build2.KernelConfig)
	}

	// Send an invalid command.
	incoming4 := fmt.Sprintf(`Sender: syzkaller@googlegroups.com
Date: Tue, 15 Aug 2017 14:59:00 -0700
Message-ID: <abcdef>
Subject: title1
From: foo@bar.com
To: %v
Content-Type: text/plain

#syz bad-command
`, sender1)

	_, err = c.POST("/_ah/mail/", incoming4)
	c.expectOK(err)

	{
		msg := c.pollEmailBug()
		c.expectEQ(msg.To, []string{"foo@bar.com"})
		c.expectEQ(msg.Subject, "Re: title1")
		c.expectEQ(msg.Headers["In-Reply-To"], []string{"<abcdef>"})
		if !strings.Contains(msg.Body, `> #syz bad-command

unknown command "bad-command"
`) {
			t.Fatal("no unknown command reply for bad command")
		}
	}

	// Now mark the bug as fixed.
	c.incomingEmail(sender1, "#syz fix: some: commit title", EmailOptCC(nil))
	reply := c.pollEmailBug().Body
	// nolint: lll
	c.expectEQ(reply, `> #syz fix: some: commit title

Your 'fix:' command is accepted, but please keep bugs@syzkaller.com mailing list in CC next time. It serves as a history of what happened with each bug report. Thank you.

`)

	// Check that the commit is now passed to builders.
	builderPollResp, _ := c.client2.BuilderPoll(build.Manager)
	c.expectEQ(len(builderPollResp.PendingCommits), 1)
	c.expectEQ(builderPollResp.PendingCommits[0], "some: commit title")

	build3 := testBuild(3)
	build3.Manager = build.Manager
	build3.Commits = []string{"some: commit title"}
	c.client2.UploadBuild(build3)

	build4 := testBuild(4)
	build4.Manager = build2.Manager
	build4.Commits = []string{"some: commit title"}
	c.client2.UploadBuild(build4)

	// New crash must produce new bug in the first reporting.
	c.client2.ReportCrash(crash)
	{
		msg := c.pollEmailBug()
		c.expectEQ(msg.Subject, crash.Title+" (2)")
		c.expectNE(msg.Sender, sender0)
	}
}

// Bug must not be mailed to maintainers if maintainers list is empty.
func TestEmailNoMaintainers(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	crash := testCrash(build, 1)
	c.client2.ReportCrash(crash)

	sender := c.pollEmailBug().Sender

	incoming1 := fmt.Sprintf(`Sender: syzkaller@googlegroups.com
Date: Tue, 15 Aug 2017 14:59:00 -0700
Message-ID: <1234>
Subject: crash1
From: %v
To: foo@bar.com
Content-Type: text/plain

#syz upstream
`, sender)
	_, err := c.POST("/_ah/mail/", incoming1)
	c.expectOK(err)
}

// Basic dup scenario: mark one bug as dup of another.
func TestEmailDup(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	crash1 := testCrash(build, 1)
	crash1.Title = "BUG: slightly more elaborate title"
	c.client2.ReportCrash(crash1)

	crash2 := testCrash(build, 2)
	crash2.Title = "KASAN: another title"
	c.client2.ReportCrash(crash2)

	msg1 := c.pollEmailBug()
	msg2 := c.pollEmailBug()

	// Dup crash2 to crash1.
	c.incomingEmail(msg2.Sender, "#syz dup: BUG: slightly more elaborate title")
	c.expectNoEmail()

	// Second crash happens again.
	crash2.ReproC = []byte("int main() {}")
	c.client2.ReportCrash(crash2)
	c.expectNoEmail()

	// Now close the original bug, and check that new bugs for dup are now created.
	c.incomingEmail(msg1.Sender, "#syz invalid")

	// "uncc" command must not trugger error reply even for closed bug.
	c.incomingEmail(msg1.Sender, "#syz uncc", EmailOptCC(nil))
	c.expectNoEmail()

	// New crash must produce new bug in the first reporting.
	c.client2.ReportCrash(crash2)
	{
		msg := c.pollEmailBug()
		c.expectEQ(msg.Subject, crash2.Title+" (2)")
	}
}

func TestEmailDup2(t *testing.T) {
	for i := 0; i < 3; i++ {
		i := i
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c := NewCtx(t)
			defer c.Close()

			build := testBuild(1)
			c.client2.UploadBuild(build)

			crash1 := testCrash(build, 1)
			crash1.Title = "BUG: something bad"
			c.client2.ReportCrash(crash1)
			msg1 := c.pollEmailBug()
			c.incomingEmail(msg1.Sender, "#syz upstream")
			msg1 = c.pollEmailBug()
			c.expectEQ(msg1.Subject, "[syzbot] BUG: something bad")

			crash2 := testCrash(build, 2)
			crash2.Title = "KASAN: another bad thing"
			c.client2.ReportCrash(crash2)
			msg2 := c.pollEmailBug()
			c.incomingEmail(msg2.Sender, "#syz upstream")
			msg2 = c.pollEmailBug()
			c.expectEQ(msg2.Subject, "[syzbot] KASAN: another bad thing")

			switch i {
			case 0:
				c.incomingEmail(msg2.Sender, "#syz dup: BUG: something bad")
			case 1:
				c.incomingEmail(msg2.Sender, "#syz dup: [syzbot] BUG: something bad")
			default:
				c.incomingEmail(msg2.Sender, "#syz dup: syzbot: BUG: something bad")
				reply := c.pollEmailBug()
				c.expectTrue(strings.Contains(reply.Body, "can't find the dup bug"))
			}
		})
	}
}

func TestEmailUndup(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	crash1 := testCrash(build, 1)
	crash1.Title = "BUG: slightly more elaborate title"
	c.client2.ReportCrash(crash1)

	crash2 := testCrash(build, 2)
	crash1.Title = "KASAN: another title"
	c.client2.ReportCrash(crash2)

	msg1 := c.pollEmailBug()
	msg2 := c.pollEmailBug()

	// Dup crash2 to crash1.
	c.incomingEmail(msg2.Sender, "#syz dup BUG: slightly more elaborate title")
	c.expectNoEmail()

	// Undup crash2.
	c.incomingEmail(msg2.Sender, "#syz undup")
	c.expectNoEmail()

	// Now close the original bug, and check that new crashes for the dup does not create bugs.
	c.incomingEmail(msg1.Sender, "#syz invalid")
	c.client2.ReportCrash(crash2)
	c.expectNoEmail()
}

func TestEmailCrossReportingDup(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	tests := []struct {
		bug    int
		dup    int
		result bool
	}{
		{0, 0, true},
		{0, 1, false},
		{0, 2, false},
		{1, 0, false},
		{1, 1, true},
		{1, 2, true},
		{2, 0, false},
		{2, 1, false},
		{2, 2, true},
	}
	for i, test := range tests {
		t.Logf("duping %v->%v, expect %v", test.bug, test.dup, test.result)
		c.advanceTime(24 * time.Hour) // to not hit email limit per day
		crash1 := testCrash(build, 1)
		crash1.Title = fmt.Sprintf("bug_%v", i)
		c.client2.ReportCrash(crash1)
		bugSender := c.pollEmailBug().Sender
		for j := 0; j < test.bug; j++ {
			c.incomingEmail(bugSender, "#syz upstream")
			bugSender = c.pollEmailBug().Sender
		}

		crash2 := testCrash(build, 2)
		crash2.Title = fmt.Sprintf("dup_%v", i)
		c.client2.ReportCrash(crash2)
		dupSender := c.pollEmailBug().Sender
		for j := 0; j < test.dup; j++ {
			c.incomingEmail(dupSender, "#syz upstream")
			dupSender = c.pollEmailBug().Sender
		}

		c.incomingEmail(bugSender, "#syz dup: "+crash2.Title)
		if test.result {
			c.expectNoEmail()
		} else {
			msg := c.pollEmailBug()
			if !strings.Contains(msg.Body, "> #syz dup:") ||
				!strings.Contains(msg.Body, "Can't dup bug to a bug in different reporting") {
				c.t.Fatalf("bad reply body:\n%v", msg.Body)
			}
		}
	}
}

func TestEmailErrors(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	// No reply for email without bug hash and no commands.
	c.incomingEmail("syzbot@testapp.appspotmail.com", "Investment Proposal")
	c.expectNoEmail()

	// If email contains a command we need to reply.
	c.incomingEmail("syzbot@testapp.appspotmail.com", "#syz invalid")
	reply := c.pollEmailBug()
	c.expectEQ(reply.To, []string{"default@sender.com"})
	c.expectEQ(reply.Body, `> #syz invalid

I see the command but can't find the corresponding bug.
Please resend the email to syzbot+HASH@testapp.appspotmail.com address
that is the sender of the bug report (also present in the Reported-by tag).

`)

	c.incomingEmail("syzbot+123@testapp.appspotmail.com", "#syz invalid")
	reply = c.pollEmailBug()
	c.expectEQ(reply.Body, `> #syz invalid

I see the command but can't find the corresponding bug.
The email is sent to  syzbot+HASH@testapp.appspotmail.com address
but the HASH does not correspond to any known bug.
Please double check the address.

`)
}

func TestEmailFailedBuild(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	failedBuild := testBuild(10)
	failedBuild.KernelRepo = testConfig.Namespaces["test2"].Repos[0].URL
	failedBuild.KernelBranch = testConfig.Namespaces["test2"].Repos[0].Branch
	failedBuild.KernelCommit = "kern2"
	failedBuild.KernelCommitTitle = "failed build 1"
	failedBuild.SyzkallerCommit = "syz2"
	buildErrorReq := &dashapi.BuildErrorReq{
		Build: *failedBuild,
		Crash: dashapi.Crash{
			Title:       "failed build 1",
			Report:      []byte("report line 1\nreport line 2\n"),
			Log:         []byte("log line 1\nlog line 2\n"),
			Maintainers: []string{"maintainer@crash"},
		},
	}
	c.expectOK(c.client2.ReportBuildError(buildErrorReq))

	msg := c.pollEmailBug()
	sender, extBugID, err := email.RemoveAddrContext(msg.Sender)
	c.expectOK(err)
	_, dbCrash, dbBuild := c.loadBug(extBugID)
	crashLogLink := externalLink(c.ctx, textCrashLog, dbCrash.Log)
	kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
	c.expectEQ(sender, fromAddr(c.ctx))
	c.expectEQ(msg.To, []string{
		"always@cc.me",
		"test@syzkaller.com",
	})
	c.expectEQ(msg.Subject, buildErrorReq.Crash.Title)
	c.expectEQ(len(msg.Attachments), 0)
	c.expectEQ(msg.Body, fmt.Sprintf(`Hello,

syzbot found the following issue on:

HEAD commit:    kern2 failed build 1
git tree:       repo10alias
console output: %[2]v
kernel config:  %[3]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler10
CC:             [maintainer@crash maintainers@repo10.org bugs@repo10.org build-maintainers@repo10.org]

IMPORTANT: if you fix the issue, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com

report line 1
report line 2


---
This report is generated by a bot. It may contain errors.
See https://goo.gl/tpsmEJ for more information about syzbot.
syzbot engineers can be reached at syzkaller@googlegroups.com.

syzbot will keep track of this issue. See:
https://goo.gl/tpsmEJ#status for how to communicate with syzbot.`,
		extBugID, crashLogLink, kernelConfigLink))
}

// Test for unfix command which should unmark a bug as fixed by any commits.
func TestEmailUnfix(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	crash := testCrash(build, 1)
	c.client2.ReportCrash(crash)

	msg := c.pollEmailBug()

	c.incomingEmail(msg.Sender, "#syz fix: some commit")
	c.expectNoEmail()
	c.incomingEmail(msg.Sender, "#syz unfix")
	c.expectNoEmail()

	build2 := testBuild(2)
	build2.Manager = build.Manager
	build2.Commits = []string{"some commit"}
	c.client2.UploadBuild(build2)

	// The bug should be still unfixed, since we unmarked it.
	c.client2.ReportCrash(crash)
	c.expectNoEmail()
}

func TestEmailManagerCC(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	// Test that we add manager CC.
	build1 := testBuild(1)
	build1.Manager = specialCCManager
	c.client2.UploadBuild(build1)

	crash := testCrash(build1, 1)
	c.client2.ReportCrash(crash)

	msg := c.pollEmailBug()
	c.expectEQ(msg.To, []string{
		"always@manager.org",
		"test@syzkaller.com",
	})

	// Test that we add manager maintainers.
	c.incomingEmail(msg.Sender, "#syz upstream")
	msg = c.pollEmailBug()
	c.expectEQ(msg.To, []string{
		"always@manager.org",
		"bugs@syzkaller.com",
		"default@maintainers.com",
		"maintainers@manager.org",
	})

	// Test that we add manager build maintainers.
	build2 := testBuild(2)
	build2.Manager = specialCCManager
	buildErrorReq := &dashapi.BuildErrorReq{
		Build: *build2,
		Crash: dashapi.Crash{
			Title:  "failed build 1",
			Report: []byte("report\n"),
			Log:    []byte("log\n"),
		},
	}
	c.expectOK(c.client2.ReportBuildError(buildErrorReq))
	msg = c.pollEmailBug()
	c.expectEQ(msg.To, []string{
		"always@manager.org",
		"test@syzkaller.com",
	})

	c.incomingEmail(msg.Sender, "#syz upstream")
	msg = c.pollEmailBug()
	c.expectEQ(msg.To, []string{
		"always@manager.org",
		"bugs@syzkaller.com",
		"build-maintainers@manager.org",
		"default@maintainers.com",
		"maintainers@manager.org",
	})

	// Test that we don't add manager CC when the crash happened on 1+ managers.
	build3 := testBuild(3)
	build1.Manager = specialCCManager
	c.client2.UploadBuild(build3)
	crash = testCrash(build3, 2)
	c.client2.ReportCrash(crash)

	build4 := testBuild(4)
	c.client2.UploadBuild(build4)
	crash = testCrash(build4, 2)
	c.client2.ReportCrash(crash)

	msg = c.pollEmailBug()
	c.expectEQ(msg.To, []string{
		"test@syzkaller.com",
	})

	c.incomingEmail(msg.Sender, "#syz upstream")
	msg = c.pollEmailBug()
	c.expectEQ(msg.To, []string{
		"bugs@syzkaller.com",
		"default@maintainers.com",
	})
}

func TestStraceReport(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	crash := testCrash(build, 1)
	crash.Flags = dashapi.CrashUnderStrace
	crash.Maintainers = []string{`"Foo Bar" <foo@bar.com>`, `bar@foo.com`, `idont@want.EMAILS`}
	c.client2.ReportCrash(crash)

	// Report the crash over email and check all fields.
	msg := c.pollEmailBug()
	_, extBugID, err := email.RemoveAddrContext(msg.Sender)
	c.expectOK(err)
	_, dbCrash, dbBuild := c.loadBug(extBugID)
	crashLogLink := externalLink(c.ctx, textCrashLog, dbCrash.Log)
	kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
	c.expectEQ(msg.Body, fmt.Sprintf(`Hello,

syzbot found the following issue on:

HEAD commit:    111111111111 kernel_commit_title1
git tree:       repo1 branch1
console+strace: %[2]v
kernel config:  %[3]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler1
CC:             [bar@foo.com foo@bar.com idont@want.EMAILS]

Unfortunately, I don't have any reproducer for this issue yet.

IMPORTANT: if you fix the issue, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com

report1

---
This report is generated by a bot. It may contain errors.
See https://goo.gl/tpsmEJ for more information about syzbot.
syzbot engineers can be reached at syzkaller@googlegroups.com.

syzbot will keep track of this issue. See:
https://goo.gl/tpsmEJ#status for how to communicate with syzbot.`,
		extBugID, crashLogLink, kernelConfigLink))
	c.checkURLContents(crashLogLink, crash.Log)
}

func TestSubjectTitleParser(t *testing.T) {
	tests := []struct {
		inSubject string
		outTitle  string
		outSeq    int
	}{
		{
			inSubject: "Re: kernel BUG in blk_mq_dispatch_rq_list (4)",
			outTitle:  "kernel BUG in blk_mq_dispatch_rq_list",
			outSeq:    3,
		},
		{
			inSubject: "Re: [syzbot] kernel BUG in blk_mq_dispatch_rq_list (4)",
			outTitle:  "kernel BUG in blk_mq_dispatch_rq_list",
			outSeq:    3,
		},
		{
			// Make sure we always take the (number) at the end.
			inSubject: "Re: kernel BUG in blk_mq_dispatch_rq_list(6) (4)",
			outTitle:  "kernel BUG in blk_mq_dispatch_rq_list(6)",
			outSeq:    3,
		},
		{
			inSubject: "RE: kernel BUG in blk_mq_dispatch_rq_list",
			outTitle:  "kernel BUG in blk_mq_dispatch_rq_list",
			outSeq:    0,
		},
		{
			// Make sure we trim the title.
			inSubject: "RE:  kernel BUG in blk_mq_dispatch_rq_list ",
			outTitle:  "kernel BUG in blk_mq_dispatch_rq_list",
			outSeq:    0,
		},
		{
			inSubject: "Re: ",
			outTitle:  "",
			outSeq:    0,
		},
	}

	p := subjectTitleParser{}
	for _, test := range tests {
		title, seq, err := p.parseTitle(test.inSubject)
		if test.outTitle == "" {
			if err == nil {
				t.Fatalf("subj: %q, expected error, got none (%q)", test.inSubject, title)
			}
		} else if title != test.outTitle {
			t.Fatalf("subj: %q, expected title=%q, got %q", test.inSubject, test.outTitle, title)
		} else if seq != test.outSeq {
			t.Fatalf("subj: %q, expected seq=%q, got %q", test.inSubject, test.outSeq, seq)
		}
	}
}

func TestBugFromSubjectInference(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	client := c.makeClient(clientPublicEmail, keyPublicEmail, true)
	client2 := c.makeClient(clientPublicEmail2, keyPublicEmail2, true)

	build := testBuild(1)
	client.UploadBuild(build)

	build2 := testBuild(2)
	client2.UploadBuild(build2)

	const crashTitle = "WARNING in corrupted"
	upstreamCrash := func(client *apiClient, build *dashapi.Build, title string) string {
		// Upload some garbage crashes.
		crash := testCrash(build, 1)
		crash.Title = title
		crash.Log = []byte(fmt.Sprintf("log%v", title))
		crash.Maintainers = []string{"maintainer@kernel.org"}
		client.ReportCrash(crash)

		sender := c.pollEmailBug().Sender
		c.incomingEmail(sender, "#syz upstream\n")

		return c.pollEmailBug().Sender
	}

	upstreamCrash(client, build, "unrelated crash")
	origSender := upstreamCrash(client, build, crashTitle)
	upstreamCrash(client, build, "unrelated crash 2")

	mailingList := "<" + config.Namespaces["access-public-email"].Reporting[0].Config.(*EmailConfig).Email + ">"

	// First try to ping some non-existing bug.
	subject := "Re: unknown-bug"
	c.incomingEmail("bugs@syzkaller.com",
		"#syz test: git://git.git/git.git kernel-branch\n"+sampleGitPatch,
		EmailOptOrigFrom("test@requester.com"),
		EmailOptFrom(mailingList), EmailOptSubject(subject),
	)
	syzbotReply := c.pollEmailBug()
	c.expectNE(syzbotReply.Sender, origSender)
	c.expectEQ(strings.Contains(syzbotReply.Body, "can't find the corresponding bug"), true)

	// Now try to test the exiting bug, but with the wrong mailing list.
	subject = "Re: " + crashTitle
	c.incomingEmail("bugs@syzkaller.com",
		"#syz test: git://git.git/git.git kernel-branch\n"+sampleGitPatch,
		EmailOptOrigFrom("test@requester.com"),
		EmailOptFrom("<unknown-list@syzkaller.com>"), EmailOptSubject(subject),
	)
	body := c.pollEmailBug().Body
	c.expectEQ(strings.Contains(body, "can't find the corresponding bug"), true)

	// Now try to test the exiting bug with the proper mailing list.
	c.incomingEmail("bugs@syzkaller.com",
		"#syz test: git://git.git/git.git kernel-branch\n"+sampleGitPatch,
		EmailOptFrom(mailingList), EmailOptOrigFrom("test@requester.com"),
		EmailOptSubject(subject),
	)
	syzbotReply = c.pollEmailBug()
	c.expectEQ(syzbotReply.Sender, origSender)
	c.expectEQ(strings.Contains(syzbotReply.Body, "This crash does not have a reproducer"), true)

	// Test that a different type of email headers is also parsed fine.
	c.incomingEmail("bugs@syzkaller.com",
		"#syz test: git://git.git/git.git kernel-branch\n"+sampleGitPatch,
		EmailOptSender(mailingList), EmailOptFrom("test@requester.com"),
		EmailOptSubject(subject),
	)
	body = c.pollEmailBug().Body
	c.expectEQ(strings.Contains(body, "This crash does not have a reproducer"), true)

	// Upstream a same-titled bug in another namespace.
	upstreamCrash(client2, build2, crashTitle)

	// Ensure that the inference fails with the proper title.
	c.incomingEmail("bugs@syzkaller.com",
		"#syz test: git://git.git/git.git kernel-branch\n"+sampleGitPatch,
		EmailOptSender(mailingList), EmailOptFrom("test@requester.com"),
		EmailOptSubject(subject),
	)
	body = c.pollEmailBug().Body
	c.expectEQ(strings.Contains(body, "Several bugs with the exact same title"), true)

	// Close the existing bug.
	c.incomingEmail("bugs@syzkaller.com", "#syz invalid",
		EmailOptFrom("test@requester.com"), EmailOptSubject(subject),
		EmailOptCC([]string{mailingList, origSender}),
	)
	c.expectNoEmail()

	// Create the (2) of the bug.
	upstreamCrash(client, build, crashTitle)

	// Make sure syzbot can understand the (2) version.
	subject = "Re: " + crashTitle + " (2)"
	c.incomingEmail("bugs@syzkaller.com",
		"#syz test: git://git.git/git.git kernel-branch\n"+sampleGitPatch,
		EmailOptFrom(mailingList), EmailOptOrigFrom("<test@requester.com>"),
		EmailOptSubject(subject),
	)
	email := c.pollEmailBug()
	c.expectEQ(email.To, []string{"test@requester.com"})
	c.expectEQ(strings.Contains(email.Body, "This crash does not have a reproducer"), true)
}

// nolint: funlen
func TestEmailLinks(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)

	crash := testCrash(build, 1)
	crash.Maintainers = []string{`"Foo Bar" <foo@bar.com>`}
	c.client2.ReportCrash(crash)

	// Report the crash over email.
	msg := c.pollEmailBug()

	// Emulate receive of the report from a mailing list.
	// This should update the bug with the link/Message-ID.
	// nolint: lll
	incoming1 := fmt.Sprintf(`Sender: syzkaller@googlegroups.com
Date: Tue, 15 Aug 2017 14:59:00 -0700
Message-ID: <1234>
Subject: crash1
From: %v
To: foo@bar.com
Content-Type: text/plain

Hello

syzbot will keep track of this issue.
If you forgot to add the Reported-by tag, once the fix for this bug is merged
into any tree, please reply to this email with:
#syz fix: exact-commit-title
To mark this as a duplicate of another syzbot report, please reply with:
#syz dup: exact-subject-of-another-report
If it's a one-off invalid bug report, please reply with:
#syz invalid

-- 
You received this message because you are subscribed to the Google Groups "syzkaller" group.
To unsubscribe from this group and stop receiving emails from it, send an email to syzkaller+unsubscribe@googlegroups.com.
To post to this group, send email to syzkaller@googlegroups.com.
To view this discussion on the web visit https://groups.google.com/d/msgid/syzkaller/1234@google.com.
For more options, visit https://groups.google.com/d/optout.
`, msg.Sender)

	_, err := c.POST("/_ah/mail/", incoming1)
	c.expectOK(err)

	_, extBugID, err := email.RemoveAddrContext(msg.Sender)
	c.expectOK(err)

	// Make sure Link is set for the last Reporting.
	dbBug, _, _ := c.loadBug(extBugID)
	reporting := lastReportedReporting(dbBug)
	c.expectNE(reporting.Link, "")
}

func TestEmailPatchTestingAccess(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	client := c.client2

	build := testBuild(1)
	client.UploadBuild(build)

	crash := testCrash(build, 1)
	client.ReportCrash(crash)

	sender := c.pollEmailBug().Sender
	c.incomingEmail(sender,
		"#syz test: git://git.git/git.git kernel-branch\n"+sampleGitPatch,
		EmailOptFrom("user@kernel.org"), EmailOptSubject("Re: "+crash.Title),
	)

	// We expect syzbot to just ignore this patch testing request.
	c.expectNoEmail()

	// The patch test job should also not be created.
	pollResp := client.pollJobs(build.Manager)
	c.expectEQ(pollResp.ID, "")
}
