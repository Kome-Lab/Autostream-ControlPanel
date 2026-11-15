//go:build lifecycle_product_integration

package lifecyclefoundation_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/httpapi"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	va "github.com/example/autostream-control-panel/internal/v3authorization"
)

func TestB1StartupAndRequiredProvider(t *testing.T){
	for _,off:=range []string{"","off","false","0"} { require(t,lf.ProductStartup(off)) }
	for _,on:=range []string{"true","1","synthetic","unknown"} { check(t,lf.ProductStartup(on)!=nil,"startup admitted an unqualified provider") }
	g:=lf.NewRequiredGate()
	called:=false
	err:=g.Do(context.Background(),lf.Call{Operation:"authority.read",Purpose:"CURRENT_READ"},func(context.Context)error{called=true;return nil})
	check(t,err!=nil&&!called,"required provider became successful")
	f:=newProductFixture(t)
	closed:=va.NewSQLAuthority(f.db).WithLifecycleGate(nil)
	_,err=closed.Save(context.Background(),1,[]va.AuthorityRecord{f.changedHost()})
	check(t,err!=nil&&f.generation(t)==1,"required nil fell back to legacy Save")
	legacy:=va.NewSQLAuthority(f.db)
	decision,err:=legacy.Read(context.Background(),f.actor.Principal,f.intent)
	require(t,err);check(t,decision.State=="ALLOWED","unconfigured legacy path lost existing rights")
}

func TestB1AuthorityReadersAndCAS(t *testing.T){
	t.Run("readers-and-save",func(t *testing.T){
		f:=newProductFixture(t);ctx:=context.Background()
		decision,err:=f.a.Read(ctx,f.actor.Principal,f.intent);require(t,err);check(t,decision.State=="ALLOWED","reader failed")
		_,err=f.a.ResolveTarget(ctx,f.intent);require(t,err)
		_,err=f.a.ResolvePolicyTrust(ctx,f.intent.Scope,va.Action);require(t,err)
		generation,err:=f.a.Generation(ctx);require(t,err);check(t,generation==1,"read advanced C epoch")
		check(t,f.attempts(t,"authority.read")==1&&f.attempts(t,"authority.target")==1&&f.attempts(t,"authority.policy")==1&&f.attempts(t,"authority.generation")==1,"reader skipped B0 participation")
		next,err:=f.a.Save(ctx,1,[]va.AuthorityRecord{f.changedHost()});require(t,err);check(t,next==2&&f.generation(t)==2,"owner CAS not shared")
		check(t,f.attempts(t,"authority.save")==1,"Save has no participant")
	})
	t.Run("stale-generation-rolls-back",func(t *testing.T){
		f:=newProductFixture(t)
		_,err:=f.a.Save(context.Background(),7,[]va.AuthorityRecord{f.changedHost()})
		check(t,err!=nil&&f.generation(t)==1&&f.attempts(t,"authority.save")==0,"bad CAS saved or succeeded")
		var unknown int;require(t,f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_lifecycle_unknowns WHERE deployment=?",f.env.Binding.Deployment).Scan(&unknown));check(t,unknown==1,"failed reserved attempt lost quarantine")
	})
}

type productHTTPSession struct { store.AuthStore; actor va.ManagementActor }
func (s *productHTTPSession) GetSession(_ context.Context,token string)(store.Session,error){
	if token!="synthetic-session" { return store.Session{},store.ErrUnauthorized }
	return store.Session{UserID:s.actor.Principal.ID,TokenHash:s.actor.SessionHash,CSRFTokenHash:security.HashToken("synthetic-csrf")},nil
}
func (s *productHTTPSession) GetUser(context.Context,string)(store.User,error){return store.User{ID:s.actor.Principal.ID,Status:"active",Roles:[]string{"super_admin"}},nil}
func (s *productHTTPSession) GetUserPermissions(context.Context,string)([]string,error){return []string{"*"},nil}

func TestB1ManagementApplyInspectHTTP(t *testing.T){
	f:=newProductFixture(t);ctx:=context.Background()
	inspected:=f.manager.Inspect(ctx,f.actor,f.metadata("inspect-1"))
	check(t,inspected.Status==va.ManagementInspected&&inspected.Generation==1,"management R did not complete")
	check(t,f.attempts(t,"management.inspect")==1,"management Inspect skipped admission")
	server:=httpapi.NewServer(nil,httpapi.WithAuthStore(&productHTTPSession{actor:f.actor}))
	handler:=server.V3AuthorityManagementHandler(f.manager)
	body,err:=json.Marshal(map[string]any{"event_id":"http-host-1","correlation_id":"http-host-1","expected_revision":1,"scope":f.intent.Scope,"action":va.Action,"records":[]va.HostRegistration{*f.changedHost().Host}});require(t,err)
	request:=httptest.NewRequest(http.MethodPost,"/v3/authority-management/hosts",strings.NewReader(string(body)))
	request.AddCookie(&http.Cookie{Name:"autostream_session",Value:"synthetic-session"})
	request.Header.Set("X-CSRF-Token","synthetic-csrf")
	response:=httptest.NewRecorder();handler.ServeHTTP(response,request)
	check(t,response.Code==http.StatusOK,"real HTTP Management Apply failed: "+response.Body.String())
	check(t,f.generation(t)==2&&f.attempts(t,"management.apply")==1,"HTTP Save did not share participation")
	var audit int;require(t,f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_management_audit WHERE event_id='http-host-1' AND result='APPLIED'").Scan(&audit));check(t,audit==1,"critical audit missing")
	check(t,f.manager.Bootstrap(ctx,f.actor,f.metadata("denied-bootstrap"),va.ManagementGrant{}).Status==va.Unavailable,"L2 Bootstrap became enabled")
}

func TestB1EvidenceStagesAndBoundFinish(t *testing.T){
	f:=newProductFixture(t)
	// Signing/clock work must occur between the completed Claim and Finish
	// participants, with no Lifecycle/C epoch lock held by this process.
	f.security.onSeal=func(){
		tx,err:=f.db.BeginTx(context.Background(),nil);require(t,err);defer tx.Rollback()
		var one int;require(t,tx.QueryRow("SELECT singleton FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE NOWAIT").Scan(&one))
		var body string;require(t,tx.QueryRow("SELECT body FROM v3_cp_lifecycle_heads WHERE deployment=? FOR UPDATE NOWAIT",f.env.Binding.Deployment).Scan(&body))
	}
	result:=f.service.Issue(context.Background(),f.actor.Principal,f.request)
	check(t,result.Status==va.Issued&&len(result.Envelope)>0&&!result.AuthorizesExecution,"synthetic issue failed or became execution authority: "+result.Code)
	check(t,f.security.seals==1&&f.attempts(t,"evidence.claim")==1&&f.attempts(t,"evidence.finish")==1,"durable issuance stages skipped admission")
	check(t,f.generation(t)==1,"issuance changed C epoch")
	lookup:=va.Lookup{Scope:f.intent.Scope,Execution:f.intent.Execution}
	read:=f.service.Read(context.Background(),f.actor.Principal,lookup)
	check(t,read.Status==va.Replay&&string(read.Envelope)==string(result.Envelope)&&!read.AuthorizesExecution,"historical read changed bytes or authority")
	check(t,f.attempts(t,"evidence.lookup")==1&&f.attempts(t,"authority.history")>=2,"read skipped current admissions")
	record,err:=f.store.Lookup(context.Background(),lookup);require(t,err)
	err=f.store.Finish(context.Background(),record)
	check(t,err!=nil,"unbound ISSUED Finish bypassed one-shot owner")
}

func TestB1EvidenceFailureAndFallback(t *testing.T){
	t.Run("claim-ack-loss-no-generation",func(t *testing.T){
		f:=newProductFixture(t)
		// Authority.Read performs its two CP commits first; fault the first
		// ledger Finalize for the Claim by arming it after Read's Complete.
		completed:=0
		f.env.AfterPort=func(op string){if op=="Complete" { completed++;if completed==1 { f.env.Fault(lf.Finalized,true) } }}
		result:=f.service.Issue(context.Background(),f.actor.Principal,f.request)
		check(t,result.Status==va.Unknown&&f.security.seals==0,"uncertain Claim continued signing")
		var state string;require(t,f.db.QueryRow("SELECT state FROM v3_cp_authorization_issuances WHERE request_id='request-1'").Scan(&state));check(t,state=="PENDING","lost ACK fabricated non-issuance")
		again:=f.service.Issue(context.Background(),f.actor.Principal,f.request)
		check(t,again.Status!=va.Issued&&f.security.seals==0,"quarantine automatically retried issuer")
	})
	t.Run("failed-generation-closes-with-participant",func(t *testing.T){
		f:=newProductFixture(t);f.security.failPrepare=true
		result:=f.service.Issue(context.Background(),f.actor.Principal,f.request)
		check(t,result.Status==va.Unavailable&&f.attempts(t,"evidence.close")==1,"closed liability bypassed participant")
		var state string;require(t,f.db.QueryRow("SELECT state FROM v3_cp_authorization_issuances WHERE request_id='request-1'").Scan(&state));check(t,state=="CLOSED","close not durable")
	})
	t.Run("partial-composition-rejected-before-claim",func(t *testing.T){
		f:=newProductFixture(t)
		service:=va.NewService(va.Config{Issuer:"cp-1",Audience:"executor"},va.Dependencies{Resolver:va.NewCanonicalBindingResolver(),Authority:f.a,Store:va.NewSQLStore(f.db),Validity:f.security,Integrity:f.security,Applicability:f.security}).WithLifecycleGate(f.env.Gate)
		result:=service.Issue(context.Background(),f.actor.Principal,f.request)
		check(t,result.Status==va.Unavailable&&f.security.seals==0,"partial B1 wiring became success")
		var count int;require(t,f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authorization_issuances").Scan(&count));check(t,count==0,"partial composition performed Claim")
	})
}

func TestB1ParticipantOwnershipAndLockOrder(t *testing.T){
	t.Run("outer-lock-prefix-and-no-ledger-under-lock",func(t *testing.T){
		f:=newProductFixture(t)
		f.env.BeforePort=func(op string){
			tx,err:=f.db.BeginTx(context.Background(),nil);require(t,err);defer tx.Rollback()
			var generation int;var body string
			require(t,tx.QueryRow("SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE NOWAIT").Scan(&generation))
			require(t,tx.QueryRow("SELECT body FROM v3_cp_lifecycle_heads WHERE deployment=? FOR UPDATE NOWAIT",f.env.Binding.Deployment).Scan(&body))
		}
		result:=f.manager.Apply(context.Background(),f.actor,f.metadata("apply-order"),[]va.AuthorityRecord{f.changedHost()},nil)
		check(t,result.Status==va.ManagementApplied,"ledger I/O observed product locks or Apply failed")
	})
	t.Run("private-owner-and-last-admin-preparation",func(t *testing.T){
		f:=newProductFixture(t);var escaped lf.Executor;var retained context.Context
		err:=f.env.Gate.Do(context.Background(),lf.Call{Operation:"identity.prepare",Purpose:"CURRENT_READ",Actor:"operator-1",Target:"operator-1",Content:"lock-order-only"},func(ctx context.Context)error{
			q,err:=lf.BeginSQL(ctx,f.db,nil);if err!=nil{return err};defer q.Rollback()
			escaped=q;retained=ctx
			var one int;var state string
			if err=q.QueryRowContext(ctx,"SELECT singleton FROM user_admin_guard WHERE singleton=1 FOR UPDATE").Scan(&one);err!=nil{return err}
			if err=q.QueryRowContext(ctx,"SELECT status FROM users WHERE id='operator-1' FOR UPDATE").Scan(&state);err!=nil{return err}
			var user string;if err=q.QueryRowContext(ctx,"SELECT user_id FROM sessions WHERE id=? FOR UPDATE",f.actor.SessionHash).Scan(&user);err!=nil{return err}
			// No late/new Gate may start from an already admitted SQL owner.
			check(t,f.env.Gate.Do(ctx,lf.Call{},func(context.Context)error{return nil})!=nil,"late Gate was accepted")
			other:=new(sql.DB);_,bound,e:=lf.SQL(ctx,other);check(t,bound&&e!=nil,"foreign DB borrowed transaction")
			return q.Commit()
		})
		require(t,err)
		_,err=escaped.ExecContext(retained,"UPDATE users SET status='disabled' WHERE id='operator-1'")
		check(t,err!=nil,"expired participant wrote")
		_,bound,err:=lf.SQL(context.Background(),f.db);check(t,!bound&&err==nil,"unbound context forged participant")
	})
	t.Run("swallowed-rollback-poisons-owner",func(t *testing.T){
		f:=newProductFixture(t)
		err:=f.env.Gate.Do(context.Background(),lf.Call{Operation:"authority.save",Purpose:"AUTHORITY_CHANGE",Target:"authority",Content:f.changedHost()},func(ctx context.Context)error{
			_,e:=f.a.Save(ctx,1,[]va.AuthorityRecord{f.changedHost()});if e!=nil{return e}
			q,e:=lf.BeginSQL(ctx,f.db,nil);if e!=nil{return e};_ = q.Rollback();return nil
		})
		check(t,err!=nil&&f.generation(t)==1,"swallowed rollback committed authority")
	})
}

func TestB1OperationalCompatibilityAndIdentityPreparation(t *testing.T){
	f:=newProductFixture(t)
	principal:=oa.Principal{ID:"operator-1",Kind:oa.KindHuman,State:oa.StateActive,RoleIDs:[]string{"custom-role","start-role"}}
	snapshot,err:=oa.Project(principal,[]oa.Role{{ID:"custom-role",Name:"custom",Operations:[]string{"roles.create"}},{ID:"start-role",Name:"starter",Operations:[]string{"streams.start"}}},1);require(t,err)
	for i:=range snapshot.Grants { if snapshot.Grants[i].Operation=="streams.start" { snapshot.Grants[i].Delegation.Mode=oa.DelegationNone } }
	memory,err:=oa.NewMemoryStore(snapshot);require(t,err)
	service:=oa.NewService(memory).WithLifecycleGate(f.env.Gate)
	_,permissions,err:=service.Current(context.Background(),"operator-1",oa.Scope{Type:oa.ScopeGlobal});require(t,err)
	check(t,len(permissions)==2,"multiple-role union shrank")
	decision,err:=service.Authorize(context.Background(),oa.Request{PrincipalID:"operator-1",Operation:"streams.start",Scope:oa.Scope{Type:oa.ScopeGlobal}});require(t,err);check(t,decision.Allowed,"DelegationNone lost own use")
	check(t,f.attempts(t,"operational.current")==1&&f.attempts(t,"operational.authorize")==1,"operational entry skipped Gate")
	closed:=oa.NewService(memory).WithLifecycleGate(nil)
	_,_,err=closed.Current(context.Background(),"operator-1",oa.Scope{Type:oa.ScopeGlobal});check(t,err!=nil,"required Operational provider fell back")
	// B2 remains unconnected: this test acquires no real identity mutation API.
	var state string;require(t,f.db.QueryRow("SELECT status FROM users WHERE id='operator-1'").Scan(&state));check(t,state=="active","preparation changed identity")
}

func TestB1UnknownRestartAndLostACK(t *testing.T){
	for _,stage:=range []lf.Phase{lf.Reserved,lf.CPCommitted,lf.Finalized,lf.ReceiptSaved,lf.Completed}{
		for _,after:=range []bool{false,true}{
			name:=string(stage)+"-rollback";if after{name=string(stage)+"-committed"}
			t.Run(name,func(t *testing.T){
				f:=newProductFixture(t);f.env.Fault(stage,after)
				next,err:=f.a.Save(context.Background(),1,[]va.AuthorityRecord{f.changedHost()})
				check(t,err!=nil&&next==0,"uncertain product Save exposed success")
				expected:=uint64(2);if stage==lf.Reserved||stage==lf.CPCommitted&&!after{expected=1}
				check(t,f.generation(t)==expected,"actual commit/rollback not distinguished")
				var id string;require(t,f.db.QueryRow("SELECT attempt FROM v3_cp_lifecycle_unknowns WHERE deployment=?",f.env.Binding.Deployment).Scan(&id))
				before:=f.generation(t)
				restarted:=lf.ResumeProductTestEnvironment(t,f.env.Binding,"operator-1",productCheckpoint)
				a:=va.NewSQLAuthority(restarted.DB).WithLifecycleGate(restarted.Gate)
				_,err=a.Save(context.Background(),1,[]va.AuthorityRecord{f.changedHost()});check(t,err!=nil&&f.generation(t)==before,"restart automatically replayed Save")
				inspection,inspectErr:=restarted.Gate.Inspect(context.Background(),"authority",id)
				if stage!=lf.Reserved||after { require(t,inspectErr);check(t,inspection.Quarantined,"Inspect lost durable unknown") } else { check(t,inspectErr!=nil,"missing reservation invented persisted result") }
				check(t,f.generation(t)==before,"Inspect changed authority")
			})
		}
	}
}


func TestB1UnreachableUnknownAndMismatch(t *testing.T) {
	for _,scenario:=range []string{"unreachable","unknown-head","cp-epoch-mismatch","inactive-local-actor"} {
		t.Run(scenario,func(t *testing.T){
			f:=newProductFixture(t)
			switch scenario {
			case "unreachable":f.env.Unavailable=true
			case "unknown-head":f.env.UnknownHead=true
			case "cp-epoch-mismatch":_,err:=f.db.Exec("UPDATE v3_cp_authority_epoch SET generation=2 WHERE singleton=1");require(t,err)
			case "inactive-local-actor":_,err:=f.db.Exec("UPDATE users SET status='disabled' WHERE id='operator-1'");require(t,err)
			}
			decision,err:=f.a.Read(context.Background(),f.actor.Principal,f.intent)
			check(t,err!=nil&&decision.State!="ALLOWED","unqualified Gate fell back to current authority")
			check(t,f.attempts(t,"authority.read")==0,"failed admission saved successful participation")
		})
	}
}
