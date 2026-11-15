package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Who may act on a campaign or a character is decided by campaign_access_2024.go alone (requireCampaignGM,
// requireCampaignMember, requireCharacterOwner, requireOwnerOrGM, requireCharacterGM, campaignRole, isCharacterGM). No
// handler compares the campaign's dm_id with the caller or writes one of the old refusal codes.
func TestNoOtherCampaignAccessCheck(t *testing.T) {
	// GM commands pick and check their campaign in gm_campaign_2024.go (a rule of its own); the others are admin, sign-in,
	// the GM view and the web pages, which don't answer a route's access question.
	exempt := map[string]bool{
		"campaign_access_2024.go": true, "gm_campaign_2024.go": true, "admin_2024.go": true, "admin_page_2024.go": true,
		"auth_and_logging.go": true, "gm_view.go": true, "campaign_flow.go": true, "web_pages.go": true, "database_init.go": true,
	}
	oldCodes := regexp.MustCompile(`"only_gm_can_|"only_dm_can_|"error":\s*"gm_only"|"not_your_character_or_campaign"|"only_the_gm_can_|"not_in_this_campaign"`)
	dmCompare := regexp.MustCompile(`\b(dmID|dmId|dm)\s*[!=]=\s*agentID\b|\bagentID\s*[!=]=\s*(dmID|dmId|dm)\b`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || exempt[f] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(b), "\n") {
			if oldCodes.MatchString(line) {
				t.Errorf("%s:%d writes an old access refusal: use the owner in campaign_access_2024.go", f, n+1)
			}
			if dmCompare.MatchString(line) {
				t.Errorf("%s:%d compares the GM with the caller by hand: use requireCampaignGM, campaignRole or isCharacterGM", f, n+1)
			}
		}
	}
}

// The helpers the old checks were written as are gone: their rule lives in campaign_access_2024.go.
func TestOldAccessHelpersGone(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	gone := regexp.MustCompile(`func (requireGMForLoss)\(`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		if m := gone.FindString(string(b)); m != "" {
			t.Errorf("%s defines %s: use requireCharacterGM / requireCharacterOwner", f, m)
		}
	}
}
