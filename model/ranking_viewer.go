package model

// rankingMaskedDisplayName is the fixed, opaque placeholder emitted for any
// user row a viewer is not allowed to identify. It is a constant so the masked
// payload never carries (directly or by derivation) a real name, a user id, or
// a stable hash of either.
const rankingMaskedDisplayName = "***"

// rankingDeletedUserLabel is shown when a usage row references an account that
// no longer exists. It carries no historical username or id so a deleted
// account stays unidentifiable.
const rankingDeletedUserLabel = "Deleted user"

// RankingViewer describes who is reading a user ranking. Masking is resolved
// on the server so an ordinary viewer never receives other users' identities,
// not even hidden metadata they could map back to an account.
type RankingViewer struct {
	UserID      int
	IsAdmin     bool
	IsAnonymous bool
}

// CanSeeName reports whether the viewer may see the given user's real name.
func (viewer RankingViewer) CanSeeName(userID int) bool {
	if viewer.IsAdmin {
		return true
	}
	if viewer.IsAnonymous || userID == 0 {
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

// VisibleUserIDs returns the user ids whose display name the viewer may see, in
// row order and de-duplicated. The caller batch-resolves only these ids.
func VisibleUserIDs(viewer RankingViewer, rows []UserRankingRow) []int {
	ids := make([]int, 0, len(rows))
	seen := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		if !viewer.CanSeeName(row.UserID) {
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

// BuildUserRankingView projects rows for the viewer. names maps a user id to
// its current display name for the ids the viewer may see; a missing entry
// means the account no longer exists and becomes the generic deleted label.
// Masked rows never consult names.
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
		}
		views = append(views, view)
	}
	return views
}
