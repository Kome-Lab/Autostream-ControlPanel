//go:build lifecycle_identity_integration

package lifecyclefoundation_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func b2Must(t *testing.T, err error) { t.Helper(); if err != nil { t.Fatal(err) } }
func b2Check(t *testing.T, yes bool, message string) { t.Helper(); if !yes { t.Fatal(message) } }
func b2Store(e *lf.IdentityTestEnvironment) store.MariaDBAuthStore { return store.NewMariaDBAuthStoreWithLifecycle(e.DB,"synthetic-test-key",e.Gate) }
func b2Projection(t *testing.T,e *lf.IdentityTestEnvironment) lf.IdentityProjection { t.Helper(); p,err:=lf.ReadIdentityProjection(context.Background(),e.DB);b2Must(t,err);return p }
func b2Count(t *testing.T, db *sql.DB, query string, args ...any) int { t.Helper();var n int;b2Must(t,db.QueryRow(query,args...).Scan(&n));return n }

func TestB2IdentityUserRoleSameTransaction(t *testing.T) {
	e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e)
	// One connection makes an accidental pool read during a private participant
	// fail the bounded operation instead of silently reading a different snapshot.
	e.DB.SetMaxOpenConns(1)
	ctx,cancel:=context.WithTimeout(context.Background(),8*time.Second);defer cancel()
	before:=b2Projection(t,e)
	u,err:=s.SetUserStatus(ctx,"member-1","disabled");b2Must(t,err)
	b2Check(t,u.ID=="member-1"&&u.Status=="disabled","committed User result was not materialized inside the owner")
	after:=b2Projection(t,e)
	b2Check(t,after.Revision==before.Revision+1&&after.AuthorityEpoch==before.AuthorityEpoch&&after.OperationalRevision>before.OperationalRevision,"User/checkpoint revisions diverged")
	b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM v3_cp_identity_history")==1,"User history did not share the commit")
	role,err:=s.CreateRole(ctx,"b2-extra",[]string{"streams.start","roles.create"});b2Must(t,err)
	b2Check(t,role.ID!=""&&len(role.Permissions)==2,"Role create lost its successful result")
	roleBefore:=b2Projection(t,e)
	role,err=s.UpdateRole(ctx,role.ID,"b2-renamed",[]string{"streams.start"});b2Must(t,err)
	roleAfter:=b2Projection(t,e)
	b2Check(t,role.Name=="b2-renamed"&&len(role.Permissions)==1,"Role readback used a stale pool snapshot")
	b2Check(t,roleAfter.OperationalRevision>roleBefore.OperationalRevision+1,"Role DML incorrectly assumed a single A increment")
	b2Check(t,roleAfter.Revision==roleBefore.Revision+1,"Role changes did not advance exactly one Identity revision")
	b2Must(t,s.DeleteRole(ctx,role.ID))
	b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM roles WHERE id=?",role.ID)==0,"Role delete escaped participation")
}

func TestB2IdentityAssignmentNilEmptyAndLastAdmin(t *testing.T) {
	t.Run("nil-versus-empty",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e)
		u,err:=s.UpdateUser(context.Background(),"member-1",store.UserPatch{Username:"renamed-member"});b2Must(t,err)
		b2Check(t,len(u.RoleIDs)==1&&u.RoleIDs[0]=="role-start","nil RoleIDs replaced the existing assignment")
		u,err=s.UpdateUser(context.Background(),"member-1",store.UserPatch{RoleIDs:[]string{}});b2Must(t,err)
		b2Check(t,len(u.RoleIDs)==0&&len(u.Roles)==0,"empty RoleIDs did not remove assignments")
	})
	t.Run("last-admin-rollback",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e);before:=b2Projection(t,e)
		u,err:=s.SetUserStatus(context.Background(),"operator-1","disabled")
		b2Check(t,errors.Is(err,lf.ErrUnknown)&&u.ID=="","reserved last-admin failure became success")
		after:=b2Projection(t,e);b2Check(t,after==before,"last-admin rollback persisted a partial projection")
		b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM users WHERE id='operator-1' AND status='active'")==1,"sole administrator was disabled")
		b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM v3_cp_identity_history")==0,"rollback appended Identity history")
		b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM v3_cp_lifecycle_unknowns WHERE deployment=?",e.Binding.Deployment)==1,"confirmed local rollback reused the external slot")
	})
}

func TestB2IdentitySessionPendingAndCurrentState(t *testing.T) {
	t.Run("pending-onboarding",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e);e.Class=lf.IdentityPending
		legacy:=store.NewMariaDBAuthStore(e.DB);snapshot,err:=legacy.GetUser(context.Background(),"pending-1");b2Must(t,err)
		session,err:=s.CompleteIdentityLogin(context.Background(),snapshot,"192.0.2.10",time.Minute,time.Hour);b2Must(t,err)
		b2Check(t,session.Token!=""&&session.CSRFToken!="","pending onboarding could not issue its limited session")
		_,err=s.GetSession(context.Background(),session.Token);b2Must(t,err)
		_,err=s.SetUserStatus(context.Background(),"member-1","disabled");b2Check(t,errors.Is(err,lf.ErrHeld),"pending context acquired an administration operation")
		b2Must(t,s.ChangeIdentityPassword(context.Background(),snapshot,"Synthetic-New-Password-2026!"))
		b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM users WHERE id='pending-1' AND status='active'")==1,"pending password change lost existing active transition")
		b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM sessions WHERE user_id='pending-1'")==0,"password change lost existing session revocation")
	})
	t.Run("refresh-current-user",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e)
		session,err:=s.CreateSession(context.Background(),"member-1",time.Minute,2*time.Minute);b2Must(t,err)
		current,err:=s.RefreshSession(context.Background(),session.Token,10*time.Minute);b2Must(t,err)
		b2Check(t,current.Token==session.Token&&current.CSRFTokenHash==session.CSRFTokenHash&&!current.IdleExpiresAt.After(current.AbsoluteExpiresAt),"refresh rotated tokens or exceeded absolute expiry")
		_,err=s.SetUserStatus(context.Background(),"member-1","disabled");b2Must(t,err)
		before:=b2Projection(t,e);denied,err:=s.RefreshSession(context.Background(),session.Token,time.Hour)
		b2Check(t,err!=nil&&denied.Token==""&&b2Projection(t,e)==before,"disabled User refreshed a retained session")
	})
	t.Run("password-stale-user",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e);snapshot,err:=store.NewMariaDBAuthStore(e.DB).GetUser(context.Background(),"member-1");b2Must(t,err)
		_,err=s.SetUserStatus(context.Background(),"member-1","disabled");b2Must(t,err)
		before:=b2Projection(t,e);err=s.ChangeIdentityPassword(context.Background(),snapshot,"Synthetic-New-Password-2026!")
		b2Check(t,err!=nil&&b2Projection(t,e)==before,"stale password proof reactivated a disabled User")
	})
}

func TestB2IdentityCascadeAndRestrictRollback(t *testing.T) {
	for _,restricted:=range []bool{false,true}{
		name:="cascade";if restricted{name="restrict"};t.Run(name,func(t *testing.T){
			e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e)
			for _,query:=range []string{
				"INSERT INTO sessions VALUES(REPEAT('a',64),'member-1',REPEAT('b',64),UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6)+INTERVAL 2 HOUR,UTC_TIMESTAMP(6))",
				"INSERT INTO oauth_user_links VALUES('link-1','member-1')",
				"INSERT INTO email_change_challenges VALUES(REPEAT('c',64),'member-1','synthetic@example.invalid',UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6))",
			}{_,err:=e.DB.Exec(query);b2Must(t,err)}
			if restricted{_,err:=e.DB.Exec("INSERT INTO b2_test_restrict_owner VALUES(1,'member-1')");b2Must(t,err)}
			e.SealFixture(t);before:=b2Projection(t,e)
			err:=s.DeleteUser(context.Background(),"member-1")
			if restricted{
				b2Check(t,err!=nil&&b2Projection(t,e)==before,"RESTRICT refusal partially committed")
				b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM sessions WHERE user_id='member-1'")==1&&b2Count(t,e.DB,"SELECT COUNT(*) FROM oauth_user_links WHERE user_id='member-1'")==1,"RESTRICT lost explicit child rows")
			}else{
				b2Must(t,err);b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM users WHERE id='member-1'")==0&&b2Count(t,e.DB,"SELECT COUNT(*) FROM email_change_challenges WHERE user_id='member-1'")==0,"User delete did not include its FK cascade")
				b2Check(t,b2Projection(t,e).Revision==before.Revision+1,"Cascade used separate Identity admissions")
			}
		})
	}
}

func TestB2IdentityACKLossRestartAndPureInspect(t *testing.T) {
	for _,stage:=range []lf.Phase{lf.Reserved,lf.CPCommitted,lf.Finalized,lf.ReceiptSaved,lf.Completed}{
		for _,after:=range []bool{false,true}{
			name:=string(stage)+"-rollback";if after{name=string(stage)+"-committed"};t.Run(name,func(t *testing.T){
				e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e);e.Fault(stage,after)
				session,err:=s.CreateSession(context.Background(),"member-1",time.Minute,time.Hour)
				b2Check(t,errors.Is(err,lf.ErrUnknown)&&session.Token==""&&session.CSRFToken==""&&session.TokenHash=="","uncertain result exposed a new session or secret")
				committed:=!(stage==lf.Reserved||stage==lf.CPCommitted&&!after)
				want:=0;if committed{want=1}
				b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM sessions")==want,"actual session commit did not match the lost-ACK boundary")
				before:=b2Projection(t,e);b2Check(t,before.Revision==uint64(want+1),"session and Identity revision were not atomic")
				var attempt string;b2Must(t,e.DB.QueryRow("SELECT attempt FROM v3_cp_lifecycle_unknowns WHERE deployment=?",e.Binding.Deployment).Scan(&attempt))
				restarted:=lf.ResumeIdentityTestEnvironment(t,e.Binding);restarted.FixedRevision=1
				denied,err:=b2Store(restarted).CreateSession(context.Background(),"member-1",time.Minute,time.Hour)
				b2Check(t,err!=nil&&denied.Token==""&&b2Projection(t,e)==before,"restart replayed the unknown session stage")
				ledgerBefore:=b2Count(t,e.DB,"SELECT COUNT(*) FROM b0_test_ledger_history WHERE deployment=?",e.Binding.Deployment)
				inspection,inspectErr:=restarted.Gate.Inspect(context.Background(),"member-1",attempt)
				if stage!=lf.Reserved||after{b2Must(t,inspectErr);b2Check(t,inspection.Quarantined,"Inspect lost the Deployment quarantine")}else{b2Check(t,inspectErr!=nil,"Inspect invented an unreserved attempt")}
				b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM b0_test_ledger_history WHERE deployment=?",e.Binding.Deployment)==ledgerBefore&&b2Projection(t,e)==before,"Inspect repaired/replayed the attempt")
				var bodies string;b2Must(t,e.DB.QueryRow("SELECT COALESCE(GROUP_CONCAT(body),'') FROM v3_cp_lifecycle_attempts WHERE deployment=?",e.Binding.Deployment).Scan(&bodies))
				b2Check(t,!strings.Contains(bodies,"synthetic-member-password")&&!strings.Contains(bodies,"csrf_token_hash"),"Ledger DTO copied synthetic secret state")
			})
		}
	}
}

func TestB2IdentityPartialRestoreAndClosedComposition(t *testing.T) {
	t.Run("partial-secret-restore",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e);_,err:=s.SetUserStatus(context.Background(),"member-1","locked");b2Must(t,err)
		before:=b2Projection(t,e)
		_,err=e.DB.Exec("UPDATE users SET password_hash='synthetic-restored-old-password' WHERE id='member-1'");b2Must(t,err)
		_,err=e.DB.Exec("UPDATE operational_authorization_epoch SET generation=? WHERE singleton=1",before.OperationalRevision);b2Must(t,err)
		b2Check(t,b2Projection(t,e)==before,"partial restore changed protected revision/count fixtures")
		result,err:=s.GetUser(context.Background(),"member-1");b2Check(t,err!=nil&&result.ID=="","equal revision/count accepted a restored secret column")
		b2Check(t,!before.Qualified,"synthetic detection qualified a real commitment profile")
	})
	t.Run("required-nil-and-missing-provider",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);closed:=store.NewMariaDBAuthStoreWithLifecycle(e.DB,"synthetic",nil)
		b2Check(t,closed.IdentityLifecycleRequired(),"nil explicit constructor fell back to legacy")
		_,err:=closed.GetUser(context.Background(),"member-1");b2Check(t,errors.Is(err,lf.ErrHeld),"required nil Gate returned a User")
		e.Unavailable=true;_,err=b2Store(e).SetUserStatus(context.Background(),"member-1","disabled")
		b2Check(t,err!=nil&&b2Count(t,e.DB,"SELECT COUNT(*) FROM users WHERE id='member-1' AND status='active'")==1,"unreachable provider fell back to legacy mutation")
	})
	t.Run("stale-revision-and-purpose",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);s:=b2Store(e);_,err:=s.SetUserStatus(context.Background(),"member-1","locked");b2Must(t,err)
		before:=b2Projection(t,e);e.FixedRevision=1;_,err=s.SetUserStatus(context.Background(),"member-1","active")
		b2Check(t,err!=nil&&b2Projection(t,e)==before,"stale proof mutated the current Identity version")
		e.FixedRevision=0;e.Class=lf.IdentitySystem;_,err=s.SetUserStatus(context.Background(),"member-1","active")
		b2Check(t,errors.Is(err,lf.ErrHeld),"System cleanup context acquired User administration")
	})
}

func TestB2IdentityPrivateParticipationAndPortLockOrder(t *testing.T) {
	t.Run("private-and-expired",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);foreign:=lf.ResumeIdentityTestEnvironment(t,e.Binding)
		var retained context.Context;var retainedSQL lf.SQLTransaction
		err:=e.Gate.DoIdentity(context.Background(),lf.IdentityCall{Operation:"identity.user.status",Target:"member-1",ContentReference:"synthetic-direct-write"},func(ctx context.Context)error{
			retained=ctx
			_,joined,err:=lf.SQL(ctx,foreign.DB);b2Check(t,joined&&errors.Is(err,lf.ErrHeld),"foreign SQL pool joined the Identity owner")
			_,err=e.Gate.RequireIdentity(ctx,"identity.user.status","another-user","synthetic-direct-write");b2Check(t,errors.Is(err,lf.ErrHeld),"another target reused Identity participation")
			_,err=e.Gate.RequireIdentity(ctx,"identity.user.status","member-1","different-effect");b2Check(t,errors.Is(err,lf.ErrHeld),"another effect reused Identity participation")
			retainedSQL,err=lf.BeginSQL(ctx,e.DB,nil);if err!=nil{return err}
			_,err=retainedSQL.ExecContext(ctx,"UPDATE users SET status='disabled' WHERE id='member-1'");if err!=nil{return err}
			probe,err:=foreign.DB.BeginTx(context.Background(),nil);b2Must(t,err)
			var value string;lockErr:=probe.QueryRow("SELECT status FROM users WHERE id='member-1' FOR UPDATE NOWAIT").Scan(&value);b2Check(t,lockErr!=nil,"Identity participant committed its User independently");b2Must(t,probe.Rollback())
			return retainedSQL.Commit()
		});b2Must(t,err)
		_,joined,err:=lf.SQL(retained,e.DB);b2Check(t,joined&&errors.Is(err,lf.ErrHeld),"retained context survived Identity callback completion")
		_,err=retainedSQL.ExecContext(retained,"UPDATE users SET status='active' WHERE id='member-1'");b2Check(t,err!=nil,"retained SQL handle replayed Identity mutation")
	})
	t.Run("external-ports-without-product-locks",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);foreign:=lf.ResumeIdentityTestEnvironment(t,e.Binding);probes:=0
		e.BeforePort=func(operation string){
			if operation=="InspectAttempt"{return};probe,err:=foreign.DB.BeginTx(context.Background(),nil);b2Must(t,err);defer probe.Rollback()
			var body,state string;var singleton int
			b2Must(t,probe.QueryRow("SELECT body FROM v3_cp_lifecycle_heads WHERE deployment=? FOR UPDATE NOWAIT",e.Binding.Deployment).Scan(&body))
			b2Must(t,probe.QueryRow("SELECT singleton FROM v3_cp_identity_projection WHERE singleton=1 FOR UPDATE NOWAIT").Scan(&singleton))
			b2Must(t,probe.QueryRow("SELECT singleton FROM user_admin_guard WHERE singleton=1 FOR UPDATE NOWAIT").Scan(&singleton))
			b2Must(t,probe.QueryRow("SELECT status FROM users WHERE id='member-1' FOR UPDATE NOWAIT").Scan(&state));probes++
		}
		_,err:=b2Store(e).SetUserStatus(context.Background(),"member-1","disabled");b2Must(t,err);b2Check(t,probes==4,"did not observe every external ledger stage")
	})
	t.Run("swallowed-rollback",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);before:=b2Projection(t,e)
		err:=e.Gate.DoIdentity(context.Background(),lf.IdentityCall{Operation:"identity.user.status",Target:"member-1"},func(ctx context.Context)error{tx,err:=lf.BeginSQL(ctx,e.DB,nil);if err!=nil{return err};_ =tx.Rollback();return nil})
		b2Check(t,errors.Is(err,lf.ErrUnknown)&&b2Projection(t,e)==before&&b2Count(t,e.DB,"SELECT COUNT(*) FROM v3_cp_identity_history")==0,"swallowed participant rollback committed Identity projection")
	})
}

func TestB2IdentitySchemaQualificationRefusesEnablement(t *testing.T) {
	for _,scenario:=range []string{"dirty","checksum","missing-append-only-trigger","unadopted-qualified-profile","wrong-engine"}{t.Run(scenario,func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);query:=""
		switch scenario{
		case "dirty":query="UPDATE v3_cp_identity_schema SET dirty=1 WHERE singleton=1"
		case "checksum":query="UPDATE v3_cp_identity_schema SET checksum=REPEAT('0',64) WHERE singleton=1"
		case "missing-append-only-trigger":query="DROP TRIGGER v3_cp_identity_history_no_update"
		case "unadopted-qualified-profile":query="UPDATE v3_cp_identity_projection SET qualified=1 WHERE singleton=1"
		case "wrong-engine":query="ALTER TABLE v3_cp_identity_projection ENGINE=MyISAM"
		}
		_,err:=e.DB.Exec(query);b2Must(t,err)
		user,err:=b2Store(e).GetUser(context.Background(),"member-1")
		b2Check(t,errors.Is(err,lf.ErrHeld)&&user.ID=="","incompatible schema/profile produced successful Identity data")
		b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM v3_cp_lifecycle_attempts WHERE deployment=?",e.Binding.Deployment)==0,"unqualified source reached external participation")
	})}
}

func TestB2IdentityRecoveryOneUseAcrossIndependentPools(t *testing.T) {
	seed:=func(t *testing.T,e *lf.IdentityTestEnvironment){t.Helper();_,err:=e.DB.Exec("INSERT INTO user_mfa(user_id,enabled,recovery_code_hashes_json,updated_at) VALUES('member-1',1,'[\"synthetic-recovery-proof-hash\"]',UTC_TIMESTAMP(6))");b2Must(t,err);e.SealFixture(t)}
	remaining:=func(t *testing.T,e *lf.IdentityTestEnvironment)string{t.Helper();var raw string;b2Must(t,e.DB.QueryRow("SELECT recovery_code_hashes_json FROM user_mfa WHERE user_id='member-1'").Scan(&raw));return raw}
	t.Run("concurrent-consume-and-reuse",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);seed(t,e);other:=lf.ResumeIdentityTestEnvironment(t,e.Binding)
		ready:=make(chan struct{},2);release:=make(chan struct{});var once sync.Once;resume:=func(){once.Do(func(){close(release)})};defer resume()
		barrier:=func(operation string){if operation=="Reserve"{ready<-struct{}{};<-release}}
		e.BeforePort=barrier;other.BeforePort=barrier;results:=make(chan error,2)
		ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second);defer cancel()
		go func(){results<-b2Store(e).ConsumeRecoveryCode(ctx,"member-1","synthetic-recovery-proof-hash")}()
		go func(){results<-b2Store(other).ConsumeRecoveryCode(ctx,"member-1","synthetic-recovery-proof-hash")}()
		for i:=0;i<2;i++{select{case <-ready:case <-time.After(10*time.Second):t.Fatal("independent Recovery consumers did not reach the Reserve barrier")}}
		resume();first,second:=<-results,<-results;e.BeforePort=nil;other.BeforePort=nil
		b2Check(t,first==nil&&second!=nil||first!=nil&&second==nil,"same Recovery Code had zero or two successful consumers")
		b2Check(t,remaining(t,e)=="[]"&&b2Projection(t,e).Revision==2&&b2Count(t,e.DB,"SELECT COUNT(*) FROM v3_cp_identity_history")==1,"one-use consume and Identity history were not one commit")
		err:=b2Store(e).ConsumeRecoveryCode(context.Background(),"member-1","synthetic-recovery-proof-hash")
		b2Check(t,err!=nil&&remaining(t,e)=="[]"&&b2Projection(t,e).Revision==2,"reused Recovery proof restored or repeated consumption")
	})
	t.Run("consume-before-later-stage-failure",func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);seed(t,e);b2Must(t,b2Store(e).ConsumeRecoveryCode(context.Background(),"member-1","synthetic-recovery-proof-hash"))
		before:=b2Projection(t,e)
		err:=e.Gate.DoIdentity(context.Background(),lf.IdentityCall{Operation:"identity.session.issue_login",Target:"member-1"},func(context.Context)error{return errors.New("synthetic later Session stage failure")})
		b2Check(t,errors.Is(err,lf.ErrUnknown)&&remaining(t,e)=="[]"&&b2Projection(t,e)==before&&b2Count(t,e.DB,"SELECT COUNT(*) FROM sessions")==0,"later Session failure undid committed Recovery consumption")
	})
}

func TestB2IdentityDormantFactorRequiredClosure(t *testing.T) {
	e:=lf.NewIdentityTestEnvironment(t);closed:=store.NewMariaDBAuthStoreWithLifecycle(e.DB,"synthetic-test-key",nil)
	for _,call:=range []struct{name string;invoke func()error}{
		{"legacy-passkey-get",func()error{_,err:=closed.GetPasskeyRegistrationChallenge(context.Background(),"synthetic-token");return err}},
		{"legacy-passkey-delete",func()error{return closed.DeletePasskeyRegistrationChallenge(context.Background(),"synthetic-token")}},
		{"ceremony-create",func()error{v,err:=closed.CreatePasskeyCeremonySession(context.Background(),"member-1","registration",[]byte("{}"),time.Minute);b2Check(t,v.Token=="","required-closed ceremony exposed a token");return err}},
		{"ceremony-get",func()error{_,err:=closed.GetPasskeyCeremonySession(context.Background(),"synthetic-token","registration");return err}},
		{"ceremony-consume",func()error{_,err:=closed.ConsumePasskeyCeremonySession(context.Background(),"synthetic-token","registration");return err}},
		{"ceremony-delete",func()error{return closed.DeletePasskeyCeremonySession(context.Background(),"synthetic-token")}},
		{"counter-write",func()error{return closed.UpdatePasskeySignCount(context.Background(),"synthetic-credential",1)}},
		{"email-create",func()error{v,err:=closed.CreateEmailChangeChallenge(context.Background(),"member-1","synthetic@example.invalid",time.Minute);b2Check(t,v.Token=="","required-closed email exposed a token");return err}},
		{"email-get",func()error{_,err:=closed.GetEmailChangeChallenge(context.Background(),"synthetic-token");return err}},
		{"email-consume",func()error{_,err:=closed.ConsumeEmailChangeChallenge(context.Background(),"synthetic-token");return err}},
		{"totp-enroll",func()error{return closed.StartTOTPEnrollment(context.Background(),"member-1","synthetic-totp-secret",[]string{"synthetic-recovery"})}},
		{"totp-confirm",func()error{return closed.ConfirmTOTPEnrollment(context.Background(),"member-1")}},
	}{t.Run(call.name,func(t *testing.T){b2Check(t,errors.Is(call.invoke(),lf.ErrHeld),"required-closed factor method bypassed lifecycle admission")})}
	b2Check(t,b2Count(t,e.DB,"SELECT COUNT(*) FROM webauthn_ceremony_sessions")==0&&b2Count(t,e.DB,"SELECT COUNT(*) FROM email_change_challenges")==0&&b2Count(t,e.DB,"SELECT COUNT(*) FROM v3_cp_identity_history")==0,"unsupported factor call performed an untracked write")
}

func TestB2IdentityLegacyFactorCannotEscapeParticipant(t *testing.T) {
	e := lf.NewIdentityTestEnvironment(t)
	legacy := store.NewMariaDBAuthStoreWithSecretKey(e.DB, "synthetic-test-key")
	err := e.Gate.DoIdentity(context.Background(), lf.IdentityCall{Operation: "identity.user.status", Target: "member-1"}, func(ctx context.Context) error {
		for _, invoke := range []func() error{
			func() error { return legacy.StartTOTPEnrollment(ctx, "member-1", "synthetic-totp-secret", nil) },
			func() error { return legacy.DeleteMFAChallenge(ctx, "synthetic-token") },
			func() error { return legacy.DeletePasskeyCredential(ctx, "member-1", "synthetic-credential") },
			func() error { _, err := legacy.ConsumeEmailChangeChallenge(ctx, "synthetic-token"); return err },
		} {
			if !errors.Is(invoke(), lf.ErrHeld) {
				return errors.New("unconfigured factor owner escaped the active private participant")
			}
		}
		return nil
	})
	b2Must(t, err)
	b2Check(t, b2Count(t, e.DB, "SELECT COUNT(*) FROM user_mfa") == 0, "unconfigured factor owner autocommitted outside the participant")
}

func TestB2IdentityConfirmedReadDenialCompletes(t *testing.T) {
	e := lf.NewIdentityTestEnvironment(t)
	_, err := e.DB.Exec("INSERT INTO sessions VALUES(?,'member-1',REPEAT('b',64),UTC_TIMESTAMP(6)-INTERVAL 2 HOUR,UTC_TIMESTAMP(6)-INTERVAL 1 HOUR,UTC_TIMESTAMP(6)-INTERVAL 3 HOUR)", security.HashToken("synthetic-expired-token"))
	b2Must(t, err)
	e.SealFixture(t)
	s := b2Store(e)
	before := b2Projection(t, e)
	for _, token := range []string{"synthetic-missing-token", "synthetic-expired-token"} {
		value, err := s.GetSession(context.Background(), token)
		b2Check(t, errors.Is(err, store.ErrNotFound) && !errors.Is(err, lf.ErrUnknown) && value.Token == "", "confirmed missing session was promoted to Lifecycle UNKNOWN")
	}
	b2Check(t, b2Projection(t, e) == before && b2Count(t, e.DB, "SELECT COUNT(*) FROM v3_cp_lifecycle_unknowns WHERE deployment=?", e.Binding.Deployment) == 0, "confirmed read denial quarantined the deployment")
	b2Check(t, b2Count(t, e.DB, "SELECT COUNT(*) FROM sessions") == 1, "read denial performed an untracked physical cleanup")
	user, err := s.GetUser(context.Background(), "member-1")
	b2Must(t, err)
	b2Check(t, user.ID == "member-1", "confirmed read denial prevented a later current read")
}
