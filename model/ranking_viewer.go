package model

// rankingMaskedDisplayName is the opaque placeholder for anonymous viewers
// and unresolved accounts. Authenticated viewers receive partial non-self names,
// without account ids or hashes that could join the row to another API.
const rankingMaskedDisplayName = "***"

// rankingDeletedUserLabel is shown when a usage row references an account that
// no longer exists. It carries no historical username or id so a deleted
// account stays unidentifiable.
const rankingDeletedUserLabel = "Deleted user"

// RankingViewer describes who is reading a user ranking. The server returns full
// names only to administrators or the account itself; other signed-in viewers
// receive partial names without hidden account identifiers.
type RankingViewer struct {
	UserID      int
	IsAdmin     bool
	IsAnonymous bool
}

// CanSeeName reports whether the viewer may see the given user's real name.
func (viewer RankingViewer) CanSeeName(userID int) bool {
	if viewer.IsAnonymous {
		return false
	}
	if viewer.IsAdmin {
		return true
	}
	if userID <= 0 {
		return false
	}
	return userID == viewer.UserID
}

// UserRankingRow is one user's aggregated usage for a model in the period. It
// carries only the user id, never a stored username, so renaming cannot
// duplicate a row and no historical name leaks through the aggregation.
type UserRankingRow struct {
	UserID   int
	Requests int64
	Tokens   int64
	Quota    int64
}

// UserRankingView is the viewer-safe projection of a ranking row. For masked
// rows UserID stays zero and is omitted from the JSON, so the payload has no
// key that could join a masked entry back to an account.
type UserRankingView struct {
	Rank        int    `json:"rank"`
	Masked      bool   `json:"masked"`
	DisplayName string `json:"display_name"`
	UserID      int    `json:"user_id,omitempty"`
	Requests    int64  `json:"requests"`
	Tokens      int64  `json:"tokens"`
	Quota       int64  `json:"quota"`
}

// VisibleUserIDs returns the ids needed for the viewer's full or partial labels,
// in row order and de-duplicated. Anonymous viewers never resolve names.
func VisibleUserIDs(viewer RankingViewer, rows []UserRankingRow) []int {
	ids := make([]int, 0, len(rows))
	if viewer.IsAnonymous || (viewer.UserID <= 0 && !viewer.IsAdmin) {
		return ids
	}
	seen := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		if row.UserID <= 0 {
			continue
		}
		if _, ok := seen[row.UserID]; ok {
			continue
		}
		seen[row.UserID] = struct{}{}
		ids = append(ids, row.UserID)
	}
	return ids
}

// BuildUserRankingView projects current names for the viewer. Administrators and
// self see full names; other authenticated viewers see only partial names. The
// mask operates on Unicode code points, never byte offsets. Anonymous viewers
// retain the opaque placeholder, and no masked row includes its account id.
func BuildUserRankingView(viewer RankingViewer, rows []UserRankingRow, names map[int]string) []UserRankingView {
	views := make([]UserRankingView, 0, len(rows))
	for index, row := range rows {
		view := UserRankingView{
			Rank:     index + 1,
			Requests: row.Requests,
			Tokens:   row.Tokens,
			Quota:    row.Quota,
		}
		if viewer.CanSeeName(row.UserID) {
			name := names[row.UserID]
			if name == "" {
				// The account no longer resolves to a username. It keeps only the
				// generic label and no id, so a deleted account stays
				// unidentifiable even for a viewer allowed to see live users.
				view.DisplayName = rankingDeletedUserLabel
			} else {
				view.UserID = row.UserID
				view.DisplayName = name
			}
		} else {
			view.Masked = true
			view.DisplayName = rankingMaskedDisplayName
			if !viewer.IsAnonymous && viewer.UserID > 0 {
				runes := []rune(names[row.UserID])
				switch len(runes) {
				case 0:
					// Keep unresolved accounts opaque.
				case 1:
					view.DisplayName = "*"
				case 2:
					view.DisplayName = string(runes[0]) + "***"
				default:
					view.DisplayName = string(runes[0]) + "***" + string(runes[len(runes)-1])
				}
			}
		}
		views = append(views, view)
	}
	return views
}
