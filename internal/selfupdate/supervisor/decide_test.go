package supervisor

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

var (
	decideNow     = time.UnixMilli(1_730_000_000_000)
	decideBuiltin = Builtin{Version: "v0.2.0", Commit: "builtin1"}
)

func release(version string) state.Target {
	return state.Target{Kind: state.KindRelease, Version: version, Commit: "c0ffee0" + version, Dir: "releases/" + version + "-c0ffee0", SchemaEpoch: 1}
}

// verifier 只认可 valid 中列出的目录，其余返回 err。
func verifier(valid ...state.Target) verifyFunc {
	return func(rel string) (state.Target, error) {
		for _, target := range valid {
			if target.Dir == rel {
				return target, nil
			}
		}
		return state.Target{}, errors.New("签名复验失败")
	}
}

func baseState() state.State {
	return state.New(state.Base{Version: decideBuiltin.Version, Commit: decideBuiltin.Commit})
}

func TestDecideRunsBuiltinForFreshState(t *testing.T) {
	d := decide(decideInput{state: state.New(state.Base{}), builtin: decideBuiltin, verify: verifier(), now: decideNow})
	if d.launch.mode != protocol.RunModeBuiltin || !d.launch.target.IsBuiltin() || d.launch.trial != nil {
		t.Fatalf("launch=%+v", d.launch)
	}
	if d.state.Base.Version != "v0.2.0" || d.consumedPending {
		t.Fatalf("state=%+v consumed=%v", d.state, d.consumedPending)
	}
}

func TestDecideRunsVerifiedActiveRelease(t *testing.T) {
	st := baseState()
	active := release("v0.3.0")
	st.Active = &active
	d := decide(decideInput{state: st, builtin: decideBuiltin, verify: verifier(active), now: decideNow})
	if d.launch.mode != protocol.RunModeRelease || d.launch.target != active {
		t.Fatalf("launch=%+v", d.launch)
	}
}

func TestDecideFallsBackWhenActiveFailsVerification(t *testing.T) {
	st := baseState()
	active := release("v0.3.0")
	st.Active = &active
	d := decide(decideInput{state: st, builtin: decideBuiltin, verify: verifier(), now: decideNow})
	if d.launch.mode != protocol.RunModeBuiltin || d.state.Active != nil {
		t.Fatalf("tampered active must fall back to builtin: launch=%+v active=%+v", d.launch, d.state.Active)
	}
	if d.state.LastResult == nil || d.state.LastResult.Outcome != state.OutcomeRejected || d.state.LastResult.Stage != state.StageVerify {
		t.Fatalf("verification failure must be recorded: %+v", d.state.LastResult)
	}
}

func TestDecideRetiresOverlaysWhenImageVersionChanges(t *testing.T) {
	for _, newImage := range []string{"v0.4.0", "v0.1.0"} {
		st := baseState()
		active := release("v0.3.0")
		st.Active = &active
		st.Previous = &state.Target{Kind: state.KindBuiltin, Version: "v0.2.0"}
		st.Trial = &state.Trial{Target: release("v0.3.1"), Action: state.ActionUpgrade, RequestID: "r"}
		d := decide(decideInput{state: st, builtin: Builtin{Version: newImage, Commit: "new"}, verify: verifier(active), now: decideNow})
		if d.launch.mode != protocol.RunModeBuiltin || d.state.Active != nil || d.state.Previous != nil || d.state.Trial != nil {
			t.Fatalf("image %s: overlays must retire, launch=%+v state=%+v", newImage, d.launch, d.state)
		}
		if d.state.Base.Version != newImage {
			t.Fatalf("image %s: base=%+v", newImage, d.state.Base)
		}
	}
	// 同一版本重新构建（commit 变化）不触发退役。
	st := baseState()
	active := release("v0.3.0")
	st.Active = &active
	d := decide(decideInput{state: st, builtin: Builtin{Version: "v0.2.0", Commit: "rebuilt"}, verify: verifier(active), now: decideNow})
	if d.launch.mode != protocol.RunModeRelease || d.state.Base.Commit != "rebuilt" {
		t.Fatalf("rebuild of the same version must keep the overlay: launch=%+v base=%+v", d.launch, d.state.Base)
	}
}

func TestDecideUseBuiltinLeavesStateAndPendingUntouched(t *testing.T) {
	st := baseState()
	active := release("v0.3.0")
	st.Active = &active
	st.Trial = &state.Trial{Target: release("v0.3.1"), Action: state.ActionUpgrade, RequestID: "r", Attempts: 1}
	pending := &state.Pending{RequestID: "p", Action: state.ActionUseBuiltin, Target: state.Target{Kind: state.KindBuiltin}}
	d := decide(decideInput{state: st, pending: pending, builtin: decideBuiltin, useBuiltin: true, verify: verifier(active), now: decideNow})
	if d.launch.mode != protocol.RunModeBuiltin || d.consumedPending {
		t.Fatalf("launch=%+v consumed=%v", d.launch, d.consumedPending)
	}
	if d.state.Active == nil || d.state.Trial == nil || d.state.Trial.Attempts != 1 {
		t.Fatalf("emergency switch must not modify active or trial: %+v", d.state)
	}
}

func TestDecideAcceptsPendingUpgradeAsTrial(t *testing.T) {
	target := release("v0.3.0")
	pending := &state.Pending{RequestID: "req-1", Action: state.ActionUpgrade, Target: target, BackupFile: "backups/pre.db", ReadyTimeoutSec: 120, RequestedBy: "admin_user"}
	d := decide(decideInput{state: baseState(), pending: pending, builtin: decideBuiltin, verify: verifier(target), now: decideNow})
	if !d.consumedPending || d.launch.mode != protocol.RunModeTrial || d.launch.trial == nil {
		t.Fatalf("launch=%+v consumed=%v", d.launch, d.consumedPending)
	}
	trial := d.state.Trial
	if trial == nil || trial.Attempts != 1 || trial.RequestID != "req-1" || trial.BackupFile != "backups/pre.db" || trial.ReadyTimeoutSec != 120 || trial.StartedAt != decideNow.UnixMilli() {
		t.Fatalf("trial=%+v", trial)
	}
}

func TestDecideRejectsInvalidPending(t *testing.T) {
	target := release("v0.3.0")
	cases := map[string]decideInput{
		"unverified directory": {pending: &state.Pending{RequestID: "a", Action: state.ActionUpgrade, Target: target, BackupFile: "b"}, verify: verifier()},
		"version mismatch": {pending: &state.Pending{RequestID: "b", Action: state.ActionUpgrade, Target: func() state.Target {
			mismatched := target
			mismatched.Version = "v0.3.1"
			return mismatched
		}(), BackupFile: "b"}, verify: verifier(target)},
		"corrupt file": {pendingErr: state.ErrCorrupt, verify: verifier()},
	}
	for name, in := range cases {
		in.state, in.builtin, in.now = baseState(), decideBuiltin, decideNow
		d := decide(in)
		if !d.consumedPending || d.launch.mode != protocol.RunModeBuiltin || d.state.Trial != nil {
			t.Fatalf("%s: launch=%+v consumed=%v trial=%+v", name, d.launch, d.consumedPending, d.state.Trial)
		}
		if d.state.LastResult == nil || d.state.LastResult.Outcome != state.OutcomeRejected {
			t.Fatalf("%s: rejection not recorded: %+v", name, d.state.LastResult)
		}
	}
	missing := decide(decideInput{state: baseState(), pendingErr: os.ErrNotExist, builtin: decideBuiltin, verify: verifier(), now: decideNow})
	if missing.consumedPending || missing.state.LastResult != nil {
		t.Fatal("a missing pending file is not a rejection")
	}
}

func TestDecideRollbackAndUseBuiltinTargets(t *testing.T) {
	st := baseState()
	active := release("v0.3.0")
	st.Active = &active
	builtinPending := &state.Pending{RequestID: "u", Action: state.ActionUseBuiltin, Target: state.Target{Kind: state.KindBuiltin}}
	d := decide(decideInput{state: st, pending: builtinPending, builtin: decideBuiltin, verify: verifier(active), now: decideNow})
	if d.launch.mode != protocol.RunModeTrial || !d.launch.target.IsBuiltin() || d.launch.target.Version != "v0.2.0" {
		t.Fatalf("use-builtin must trial the builtin version: %+v", d.launch)
	}
	previous := release("v0.2.5")
	rollback := &state.Pending{RequestID: "r", Action: state.ActionRollback, Target: previous}
	d = decide(decideInput{state: st, pending: rollback, builtin: decideBuiltin, verify: verifier(active, previous), now: decideNow})
	if d.launch.mode != protocol.RunModeTrial || d.launch.target != previous {
		t.Fatalf("rollback must trial the previous release: %+v", d.launch)
	}
}

func TestDecideGivesUpAfterRepeatedTrialAttempts(t *testing.T) {
	st := baseState()
	active := release("v0.3.0")
	st.Active = &active
	st.Trial = &state.Trial{Target: release("v0.3.1"), Action: state.ActionUpgrade, RequestID: "req-9", Attempts: maxTrialAttempts, BackupFile: "backups/pre.db"}
	d := decide(decideInput{state: st, builtin: decideBuiltin, verify: verifier(active, release("v0.3.1")), now: decideNow})
	if d.launch.mode != protocol.RunModeRelease || d.launch.target != active || d.state.Trial != nil {
		t.Fatalf("after %d attempts the trial must roll back to active: launch=%+v", maxTrialAttempts, d.launch)
	}
	if !d.state.IsQuarantined("v0.3.1", "") || d.state.LastResult.Outcome != state.OutcomeRolledBack {
		t.Fatalf("failed upgrade must be quarantined: %+v", d.state)
	}
	if d.restore == nil || d.restore.backupFile != "backups/pre.db" || d.restore.requestID != "req-9" {
		t.Fatalf("restore candidate=%+v", d.restore)
	}
}

func TestDecideRollsBackTrialWhoseReleaseFailsVerification(t *testing.T) {
	st := baseState()
	st.Trial = &state.Trial{Target: release("v0.3.1"), Action: state.ActionUpgrade, RequestID: "req", Attempts: 0}
	d := decide(decideInput{state: st, builtin: decideBuiltin, verify: verifier(), now: decideNow})
	if d.launch.mode != protocol.RunModeBuiltin || d.state.Trial != nil || d.state.LastResult.Stage != state.StageVerify {
		t.Fatalf("launch=%+v result=%+v", d.launch, d.state.LastResult)
	}
}

func TestDecideNewPendingReplacesExistingTrial(t *testing.T) {
	st := baseState()
	st.Trial = &state.Trial{Target: release("v0.3.0"), Action: state.ActionUpgrade, RequestID: "old", Attempts: 1}
	next := release("v0.3.1")
	pending := &state.Pending{RequestID: "new", Action: state.ActionUpgrade, Target: next, BackupFile: "b"}
	d := decide(decideInput{state: st, pending: pending, builtin: decideBuiltin, verify: verifier(next), now: decideNow})
	if d.state.Trial == nil || d.state.Trial.RequestID != "new" || d.state.Trial.Attempts != 1 {
		t.Fatalf("trial=%+v", d.state.Trial)
	}
	found := false
	for _, result := range d.state.History {
		if result.RequestID == "old" && result.Outcome == state.OutcomeRejected {
			found = true
		}
	}
	if !found {
		t.Fatalf("the replaced trial must be recorded: %+v", d.state.History)
	}
}

func TestReleaseNewerThanBuiltin(t *testing.T) {
	if err := releaseNewerThanBuiltin("v0.3.0", "v0.2.0"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][2]string{{"v0.2.0", "v0.2.0"}, {"v0.1.0", "v0.2.0"}, {"v0.3.0", "dev"}} {
		if err := releaseNewerThanBuiltin(tc[0], tc[1]); err == nil || !strings.Contains(err.Error(), tc[0]) {
			t.Fatalf("release %s over builtin %s must be refused: %v", tc[0], tc[1], err)
		}
	}
}
