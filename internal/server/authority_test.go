package server

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/audit/v1/auditv1connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/grant/v1/grantv1connect"
	organizationv1 "github.com/abcdlsj/sumi/gen/go/sumi/organization/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/organization/v1/organizationv1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestAuthorityAPIAuthenticatesHumansAndEnforcesGrants(t *testing.T) {
	dataRoot := t.TempDir()
	api := openAuthorityAPI(t, dataRoot)
	defer api.close(t)
	ownerCredential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = api.organizations.GetOrganization(context.Background(), connect.NewRequest(&organizationv1.GetOrganizationRequest{}))
	assertConnectCode(t, err, connect.CodeUnauthenticated)
	organizationRequest := connect.NewRequest(&organizationv1.GetOrganizationRequest{})
	authorize(organizationRequest, ownerCredential)
	organizationResponse, err := api.organizations.GetOrganization(context.Background(), organizationRequest)
	if err != nil {
		t.Fatal(err)
	}
	organizationID := organizationResponse.Msg.GetOrganization().GetId()
	bootstrapHumanID := organizationResponse.Msg.GetOrganization().GetBootstrapHumanId()

	memberCredential := "member-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	createMember := connect.NewRequest(&organizationv1.CreateHumanRequest{
		RequestId: uuid.NewString(), Name: "Second Human", Role: organizationv1.HumanRole_HUMAN_ROLE_MEMBER, Credential: memberCredential,
	})
	authorize(createMember, ownerCredential)
	memberResponse, err := api.organizations.CreateHuman(context.Background(), createMember)
	if err != nil {
		t.Fatal(err)
	}
	memberID := memberResponse.Msg.GetHuman().GetId()
	encoded, err := protojson.Marshal(memberResponse.Msg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(memberCredential)) || bytes.Contains(encoded, []byte("credential")) {
		t.Fatalf("credential leaked in response: %s", encoded)
	}

	deniedCreate := connect.NewRequest(&organizationv1.CreateHumanRequest{
		RequestId: uuid.NewString(), Name: "Denied Human", Role: organizationv1.HumanRole_HUMAN_ROLE_MEMBER,
		Credential: "denied-credential-abcdefghijklmnopqrstuvwxyz-01234567",
	})
	authorize(deniedCreate, memberCredential)
	_, err = api.organizations.CreateHuman(context.Background(), deniedCreate)
	assertConnectCode(t, err, connect.CodePermissionDenied)

	listGrants := connect.NewRequest(&grantv1.ListGrantsRequest{})
	authorize(listGrants, ownerCredential)
	grantsResponse, err := api.grants.ListGrants(context.Background(), listGrants)
	if err != nil {
		t.Fatal(err)
	}
	if len(grantsResponse.Msg.GetGrants()) != 1 {
		t.Fatalf("bootstrap grants = %v", grantsResponse.Msg.GetGrants())
	}
	rootGrantID := grantsResponse.Msg.GetGrants()[0].GetId()
	issue := connect.NewRequest(&grantv1.IssueGrantRequest{
		RequestId:     uuid.NewString(),
		Subject:       &grantv1.Principal{Kind: grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: memberID},
		Capability:    grantv1.Capability_CAPABILITY_HUMAN_CREATE,
		Scope:         &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION, Id: organizationID},
		ParentGrantId: rootGrantID,
	})
	authorize(issue, ownerCredential)
	if _, err := api.grants.IssueGrant(context.Background(), issue); err != nil {
		t.Fatal(err)
	}

	allowedCreate := connect.NewRequest(&organizationv1.CreateHumanRequest{
		RequestId: uuid.NewString(), Name: "Allowed Human", Role: organizationv1.HumanRole_HUMAN_ROLE_MEMBER,
		Credential: "allowed-credential-abcdefghijklmnopqrstuvwxyz-0123456",
	})
	authorize(allowedCreate, memberCredential)
	if _, err := api.organizations.CreateHuman(context.Background(), allowedCreate); err != nil {
		t.Fatal(err)
	}

	auditRequest := connect.NewRequest(&auditv1.ListAuditEventsRequest{Limit: 100})
	authorize(auditRequest, ownerCredential)
	auditResponse, err := api.audit.ListAuditEvents(context.Background(), auditRequest)
	if err != nil {
		t.Fatal(err)
	}
	foundDenied := false
	for _, event := range auditResponse.Msg.GetEvents() {
		if event.GetActor().GetId() == memberID && event.GetAction() == auditv1.AuditAction_AUDIT_ACTION_HUMAN_CREATE && event.GetOutcome() == auditv1.AuditOutcome_AUDIT_OUTCOME_DENIED {
			foundDenied = true
		}
	}
	if !foundDenied {
		t.Fatalf("denied human creation missing from audit: %v", auditResponse.Msg.GetEvents())
	}

	disable := connect.NewRequest(&organizationv1.SetHumanStatusRequest{
		RequestId: uuid.NewString(), HumanId: memberID, Status: organizationv1.HumanStatus_HUMAN_STATUS_DISABLED,
	})
	authorize(disable, ownerCredential)
	if _, err := api.organizations.SetHumanStatus(context.Background(), disable); err != nil {
		t.Fatal(err)
	}
	disabledRequest := connect.NewRequest(&organizationv1.GetOrganizationRequest{})
	authorize(disabledRequest, memberCredential)
	_, err = api.organizations.GetOrganization(context.Background(), disabledRequest)
	assertConnectCode(t, err, connect.CodeUnauthenticated)

	databasePayload, err := os.ReadFile(filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databasePayload, []byte(ownerCredential)) || bytes.Contains(databasePayload, []byte(memberCredential)) {
		t.Fatal("raw credential found in database")
	}
	if bootstrapHumanID == "" || memberID == bootstrapHumanID {
		t.Fatalf("human identities = %q/%q", bootstrapHumanID, memberID)
	}
}

func TestAuthorityServerRestartRequiresOriginalCredentialFile(t *testing.T) {
	dataRoot := t.TempDir()
	api := openAuthorityAPI(t, dataRoot)
	ownerCredential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	request := connect.NewRequest(&organizationv1.GetOrganizationRequest{})
	authorize(request, ownerCredential)
	first, err := api.organizations.GetOrganization(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	organizationID := first.Msg.GetOrganization().GetId()
	api.close(t)

	api = openAuthorityAPI(t, dataRoot)
	request = connect.NewRequest(&organizationv1.GetOrganizationRequest{})
	authorize(request, ownerCredential)
	restarted, err := api.organizations.GetOrganization(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Msg.GetOrganization().GetId() != organizationID {
		t.Fatalf("organization changed across restart from %q to %q", organizationID, restarted.Msg.GetOrganization().GetId())
	}
	api.close(t)

	keyPath := filepath.Join(dataRoot, "owner.key")
	backupPath := filepath.Join(dataRoot, "owner.backup")
	if err := os.Rename(keyPath, backupPath); err != nil {
		t.Fatal(err)
	}
	if app, err := New(context.Background(), Config{DataRoot: dataRoot}); err == nil {
		app.Close()
		t.Fatal("server started without existing authority credential")
	}
	if err := os.Rename(backupPath, keyPath); err != nil {
		t.Fatal(err)
	}
	wrongCredential := "wrong-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	if err := os.WriteFile(keyPath, []byte(wrongCredential), 0o600); err != nil {
		t.Fatal(err)
	}
	if app, err := New(context.Background(), Config{DataRoot: dataRoot}); err == nil {
		app.Close()
		t.Fatal("server started with mismatched authority credential")
	} else if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched credential reported missing: %v", err)
	}
}

type authorityAPI struct {
	app           *Server
	http          *httptest.Server
	organizations organizationv1connect.OrganizationServiceClient
	grants        grantv1connect.GrantServiceClient
	audit         auditv1connect.AuditServiceClient
}

func openAuthorityAPI(t *testing.T, dataRoot string) authorityAPI {
	t.Helper()
	app, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	return authorityAPI{
		app: app, http: httpServer,
		organizations: organizationv1connect.NewOrganizationServiceClient(httpServer.Client(), httpServer.URL),
		grants:        grantv1connect.NewGrantServiceClient(httpServer.Client(), httpServer.URL),
		audit:         auditv1connect.NewAuditServiceClient(httpServer.Client(), httpServer.URL),
	}
}

func (api authorityAPI) close(t *testing.T) {
	t.Helper()
	api.http.Close()
	if err := api.app.Close(); err != nil {
		t.Fatal(err)
	}
}

func authorize[T any](request *connect.Request[T], credential string) {
	request.Header().Set("Authorization", "Bearer "+credential)
}
