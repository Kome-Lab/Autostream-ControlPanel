package security

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestControlPanelInstallerRollbackPreservesPreexistingAutostreamGroup(t *testing.T) {
	root := filepath.Join("..", "..")
	installerBytes, err := os.ReadFile(filepath.Join(
		root,
		"release",
		"install-autostream-control-panel",
	))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerBytes)

	for _, marker := range []string{
		`readonly AUTOSTREAM_USER_ROLLBACK_LOGIN="autostream-install-rollback"`,
		"prepare_autostream_user_rollback_login()",
		"local_account_member_fields_are_clear()",
		"local_account_database_matches_digests()",
		"restore_created_service_user_login()",
		"remove_created_service_user_preserving_group()",
		"created_service_user_record",
		"created_service_group_record",
		"preexisting_service_group_record",
		`usermod --login "${AUTOSTREAM_USER_ROLLBACK_LOGIN}" autostream`,
		`userdel "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
		`usermod --login autostream "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
		`sha256sum -- /etc/group`,
		`sha256sum -- /etc/gshadow`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("installer is missing pre-existing service-group rollback guard %q", marker)
		}
	}
	for _, forbidden := range []string{
		`elif ! userdel autostream; then`,
		`userdel autostream`,
		`usermod --gid`,
		`usermod --home`,
	} {
		if strings.Contains(installer, forbidden) {
			t.Fatalf("installer rollback can mutate the protected service group or user identity through %q", forbidden)
		}
	}

	restoreStart := strings.Index(installer, "restore_created_service_user_login()")
	removeStart := strings.Index(installer, "remove_created_service_user_preserving_group()")
	rollbackStart := strings.Index(installer, "rollback_created_service_account()")
	if restoreStart < 0 || removeStart < 0 || rollbackStart < 0 ||
		restoreStart >= removeStart || removeStart >= rollbackStart {
		t.Fatal("installer service-account rollback helper boundaries are invalid")
	}
	restoreBody := installer[restoreStart:removeStart]
	restoreRename := strings.Index(
		restoreBody,
		`usermod --login autostream "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
	)
	restoreDigestCheck := strings.Index(
		restoreBody,
		"local_account_database_matches_digests",
	)
	restoreGroupCheck := strings.Index(
		restoreBody,
		`getent group autostream 2>/dev/null || true) == "${expected_group_record}"`,
	)
	if restoreRename < 0 || restoreDigestCheck < 0 || restoreGroupCheck < 0 ||
		restoreRename >= restoreDigestCheck || restoreDigestCheck >= restoreGroupCheck {
		t.Fatal("installer must restore the original login before validating the protected group databases")
	}

	removeBody := installer[removeStart:rollbackStart]
	groupSnapshot := strings.Index(removeBody, "group_database_digest_before=")
	membershipCheck := strings.Index(removeBody, "local_account_member_fields_are_clear")
	forwardRename := strings.Index(
		removeBody,
		`usermod --login "${AUTOSTREAM_USER_ROLLBACK_LOGIN}" autostream`,
	)
	postRenameDigestOffset := -1
	if forwardRename >= 0 {
		postRenameDigestOffset = strings.Index(
			removeBody[forwardRename:],
			"local_account_database_matches_digests",
		)
	}
	deleteRenamed := strings.Index(
		removeBody,
		`userdel "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
	)
	finalDigestOffset := -1
	if deleteRenamed >= 0 {
		finalDigestOffset = strings.LastIndex(
			removeBody[deleteRenamed:],
			"local_account_database_matches_digests",
		)
	}
	if groupSnapshot < 0 || membershipCheck < 0 || forwardRename < 0 ||
		postRenameDigestOffset < 0 || deleteRenamed < 0 || finalDigestOffset < 0 ||
		groupSnapshot >= membershipCheck || membershipCheck >= forwardRename ||
		forwardRename+postRenameDigestOffset >= deleteRenamed ||
		deleteRenamed >= deleteRenamed+finalDigestOffset {
		t.Fatal("installer must snapshot and validate group databases, rename the user, delete only the renamed login, then revalidate")
	}
	if count := strings.Count(
		removeBody,
		`userdel "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
	); count != 1 {
		t.Fatalf("installer renamed-login deletion count = %d, want 1", count)
	}

	integrationBytes, err := readInstallerScenarioSource(filepath.Join(
		root,
		"release",
		"test-install-autostream-control-panel-integration.sh",
	))
	if err != nil {
		t.Fatal(err)
	}
	integration := string(integrationBytes)
	currentLinkDefinition := strings.Index(
		integration,
		`readonly CURRENT_LINK="${MANAGED_ROOT}/current"`,
	)
	currentLinkProbe := strings.Index(
		integration,
		`cat > "${WORK_DIR}/signal-mv" <<EOF`,
	)
	if currentLinkDefinition < 0 || currentLinkProbe < 0 ||
		currentLinkDefinition >= currentLinkProbe {
		t.Fatal("integration fixture must define CURRENT_LINK before expanding the current-link fault probe")
	}
	for _, marker := range []string{
		"useradd TERM transaction exited with",
		"useradd TERM transaction did not reach its injection boundary",
		"useradd TERM transaction changed the pre-existing service group",
		"useradd TERM transaction changed the pre-existing local group databases",
		"useradd TERM transaction left the reserved rollback login",
		"useradd TERM transaction left private input staging behind",
		"report_failed_install_probe()",
		`"${WORK_DIR}/signal-useradd-rollback.out"`,
		"fixture account teardown left the service account behind",
	} {
		if !strings.Contains(integration, marker) {
			t.Fatalf("integration fixture is missing pre-existing service-group rollback marker %q", marker)
		}
	}
	if regexp.MustCompile(`userdel autostream\s*\n\s*groupdel autostream`).MatchString(integration) {
		t.Fatal("integration fixture unconditionally deletes an autostream group that userdel may already remove")
	}
}
