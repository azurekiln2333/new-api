package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// insertListedChannel creates an enabled channel of the given type and models so
// the listing/options projections have deterministic rows to page.
func insertListedChannel(t *testing.T, name string, channelType int, models string) *model.Channel {
	t.Helper()
	channel := &model.Channel{
		Name: name, Type: channelType, Key: "sk-" + name, Models: models,
		Group: "default", Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, channel.Insert())
	return channel
}

func runChannelHandler(t *testing.T, handler gin.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(c)
	return recorder
}

// queryObserver counts the queries and the rows each query touched. It hooks
// GORM's query callbacks rather than a logger, because model.Channel.AfterFind
// runs its own reads on a logger.Discard session and a Trace-based counter would
// never see the per-row reads this test must catch.
type queryObserver struct {
	mu sync.Mutex
	// channels counts statements that read the channels table.
	channels int
	// allocations counts statements that read the allocation table.
	allocations int
	// channelRows sums the rows returned by channels-table reads.
	channelRows int
	// allocationRows sums the rows returned by allocation-table reads.
	allocationRows int
}

func (o *queryObserver) reset() {
	o.mu.Lock()
	o.channels, o.allocations, o.channelRows, o.allocationRows = 0, 0, 0, 0
	o.mu.Unlock()
}

func (o *queryObserver) snapshot() (channels, allocations, channelRows, allocationRows int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.channels, o.allocations, o.channelRows, o.allocationRows
}

// observeChannelQueries registers before/after query callbacks on the live
// handle for the duration of the test. RowsAffected is read after the statement
// runs, so the observation is per statement and independent of log level.
func observeChannelQueries(t *testing.T) *queryObserver {
	t.Helper()
	observer := &queryObserver{}
	database := model.DB
	const before, after = "test:observe-channel-reads:before", "test:observe-channel-reads:after"
	require.NoError(t, database.Callback().Query().Before("gorm:query").Register(before, func(tx *gorm.DB) {
		if tx.Statement.SQL.Len() > 0 {
			return
		}
	}))
	require.NoError(t, database.Callback().Query().After("gorm:query").Register(after, func(tx *gorm.DB) {
		table := tx.Statement.Table
		observer.mu.Lock()
		defer observer.mu.Unlock()
		switch table {
		case "channels":
			observer.channels++
			observer.channelRows += int(tx.RowsAffected)
		case "channel_key_entries":
			observer.allocations++
			observer.allocationRows += int(tx.RowsAffected)
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, database.Callback().Query().Before("gorm:query").Remove(before))
		require.NoError(t, database.Callback().Query().After("gorm:query").Remove(after))
	})
	return observer
}

// TestChannelSearchPageSizeIsCapped pins the listing bound: search accepts an
// arbitrary page_size, so the handler must cap it before the offset math. Without
// the cap one request could read the whole matching set in a single page.
func TestChannelSearchPageSizeIsCapped(t *testing.T) {
	setupChannelCreateTest(t)
	observer := observeChannelQueries(t)
	const matching = 140
	for i := range matching {
		insertListedChannel(t, "capped-row-"+strconv.Itoa(i), 1, "gpt-4o")
	}

	observer.reset()
	response := runChannelHandler(t, SearchChannels, "/api/channel/search?keyword=capped-row-&p=1&page_size=100000")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Items []model.Channel `json:"items"`
			Total int64           `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success, response.Body.String())
	assert.EqualValues(t, matching, body.Data.Total, "the total must still cover every matching channel")
	assert.Len(t, body.Data.Items, 100, "an oversized page_size must be capped before the page read")

	// The cap must be enforced by the query, not by trimming the result in memory.
	_, _, channelRows, _ := observer.snapshot()
	assert.Less(t, channelRows, matching, "a capped page must not read every matching channel row")
}

// TestChannelSearchQueryCountDoesNotGrowWithPageSize pins the loading contract the
// console relies on: a paged search reads rows in proportion to the page, not to
// the number of matching channels. The same 40 matching channels are read with a
// small page and with a large one.
//
// A regression to "load every matching channel, then slice in memory" loads 40
// channel rows for both runs, so the page-sized assertion fails. A regression to a
// per-channel allocation query inflates the allocation statement count with the
// page size or the matching row count.
func TestChannelSearchQueryCountDoesNotGrowWithPageSize(t *testing.T) {
	setupChannelCreateTest(t)
	observer := observeChannelQueries(t)
	for i := range 40 {
		insertListedChannel(t, "amp-row-"+strconv.Itoa(i), 1, "gpt-4o")
	}

	type searchBody struct {
		Success bool `json:"success"`
		Data    struct {
			Items []model.Channel `json:"items"`
			Total int64           `json:"total"`
		} `json:"data"`
	}
	measure := func(pageSize int) (body searchBody, channels, channelRows, allocations int) {
		observer.reset()
		response := runChannelHandler(t, SearchChannels, "/api/channel/search?keyword=amp-row-&p=1&page_size="+strconv.Itoa(pageSize))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		require.True(t, body.Success, response.Body.String())
		require.EqualValues(t, 40, body.Data.Total, "the SQL total must cover every matching channel")
		require.Len(t, body.Data.Items, pageSize, "the page must be bounded by page_size")
		channels, allocations, channelRows, _ = observer.snapshot()
		return body, channels, channelRows, allocations
	}

	_, smallChannels, smallChannelRows, smallAllocations := measure(2)
	_, _, largeChannelRows, largeAllocations := measure(40)

	require.Positive(t, smallChannelRows, "the search must actually read the channels table")
	// The small page must not have read more channel rows than the page plus the
	// constant bookkeeping reads; loading all 40 rows fails here.
	assert.LessOrEqual(t, smallChannelRows, 2+8,
		"a page_size=2 search must not read every matching channel row (%d rows)", smallChannelRows)
	assert.LessOrEqual(t, smallChannels, 6,
		"a page_size=2 search must issue a bounded number of channel statements (%d)", smallChannels)

	// Loading all 40 rows and slicing would report roughly the same row count for
	// both page sizes; a page-sized read stays an order of magnitude smaller.
	assert.Less(t, smallChannelRows*5, largeChannelRows,
		"channel rows must track the page, not the matching set (%d small vs %d large)", smallChannelRows, largeChannelRows)

	require.Positive(t, largeChannelRows, "the large page must read its rows")
	assert.Positive(t, smallAllocations, "allocation reads must still happen for the page")
	// The projection is chunked with a 200-row chunk size, so a page far below one
	// chunk must use exactly one allocation statement. A per-row query uses one per
	// row (2 and 40 respectively).
	assert.Equal(t, smallAllocations, largeAllocations,
		"allocation statements must be chunk-bounded, not per row (%d vs %d)", smallAllocations, largeAllocations)
	assert.LessOrEqual(t, smallAllocations, 1,
		"a page under one projection chunk must use one allocation read (%d)", smallAllocations)
}

// TestChannelSearchTypeCountsFollowStatusFilter pins that type_counts describes
// the same status-filtered set the page was counted under, rather than the whole
// table. A hardcoded all-status count would report disabled rows as enabled.
func TestChannelSearchTypeCountsFollowStatusFilter(t *testing.T) {
	setupChannelCreateTest(t)
	for i := range 3 {
		insertListedChannel(t, "counts-enabled-"+strconv.Itoa(i), 1, "gpt-4o")
	}
	insertListedChannel(t, "counts-enabled-other-type", 2, "gpt-4o")
	for i := range 2 {
		disabled := insertListedChannel(t, "counts-disabled-"+strconv.Itoa(i), 1, "gpt-4o")
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", disabled.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
	}

	type searchBody struct {
		Data struct {
			Total      int64            `json:"total"`
			TypeCounts map[string]int64 `json:"type_counts"`
		} `json:"data"`
	}
	read := func(target string) searchBody {
		response := runChannelHandler(t, SearchChannels, target)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body searchBody
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		return body
	}

	enabled := read("/api/channel/search?keyword=counts-&status=enabled")
	assert.EqualValues(t, 4, enabled.Data.Total)
	assert.EqualValues(t, 3, enabled.Data.TypeCounts["1"], "enabled type 1 count must exclude the two disabled rows")
	assert.EqualValues(t, 1, enabled.Data.TypeCounts["2"])

	disabled := read("/api/channel/search?keyword=counts-&status=disabled")
	assert.EqualValues(t, 2, disabled.Data.Total)
	assert.EqualValues(t, 2, disabled.Data.TypeCounts["1"])
	assert.NotContains(t, disabled.Data.TypeCounts, "2", "a type with no disabled rows must not appear")
}

// TestChannelSearchTagModeTotalsFollowExpandedSet pins that tag mode reports the
// expanded tag set it actually returns: a tag expands to every channel carrying
// it, and type_counts/status filtering apply to that expansion, not to a keyword
// SQL aggregate over channels that never matched a tag.
// TestChannelSearchTagModePagesInSQL pins that tag mode no longer expands every
// matching channel row before slicing the page. The same tag set is read with a
// small page and a large one; the page read must stay proportional to page_size,
// and the reported total must still describe the whole expanded set.
func TestChannelSearchTagModePagesInSQL(t *testing.T) {
	setupChannelCreateTest(t)
	observer := observeChannelQueries(t)
	// Several tags with several channels each, so a per-tag expansion would read
	// the full set rather than one page.
	const tags, perTag = 4, 10
	for tagIndex := range tags {
		tag := "tagpage-" + strconv.Itoa(tagIndex)
		for member := range perTag {
			channel := insertListedChannel(t, tag+"-"+strconv.Itoa(member), 1, "gpt-4o")
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).
				Update("tag", tag).Error)
		}
	}

	type searchBody struct {
		Success bool `json:"success"`
		Data    struct {
			Items []model.Channel `json:"items"`
			Total int64           `json:"total"`
		} `json:"data"`
	}
	measure := func(pageSize int) (body searchBody, channelRows int) {
		observer.reset()
		target := "/api/channel/search?keyword=tagpage-&tag_mode=1&p=1&page_size=" + strconv.Itoa(pageSize)
		response := runChannelHandler(t, SearchChannels, target)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		require.True(t, body.Success, response.Body.String())
		require.EqualValues(t, tags*perTag, body.Data.Total, "the total must cover the whole expanded set")
		require.Len(t, body.Data.Items, pageSize, "the page must be bounded by page_size")
		_, _, channelRows, _ = observer.snapshot()
		return body, channelRows
	}

	_, smallRows := measure(3)
	_, largeRows := measure(tags * perTag)
	require.Positive(t, smallRows, "the tag search must actually read the channels table")
	assert.Less(t, smallRows*2, largeRows, "a small page must not read the whole expanded tag set")
}

// TestChannelSearchTagModeTotalsFollowExpandedSet pins the tag-mode contract the
// console relies on: total and type_counts describe the expanded tag set after
// the status filter and before type narrowing.
func TestChannelSearchTagModeTotalsFollowExpandedSet(t *testing.T) {
	setupChannelCreateTest(t)
	tag := "tagsearch"
	withTag := func(name string, channelType int, status int) *model.Channel {
		channel := insertListedChannel(t, name, channelType, "gpt-4o")
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).
			Updates(map[string]any{"tag": tag, "status": status}).Error)
		return channel
	}
	withTag("tagsearch-a", 1, common.ChannelStatusEnabled)
	withTag("tagsearch-b", 1, common.ChannelStatusEnabled)
	withTag("tagsearch-c", 5, common.ChannelStatusManuallyDisabled)

	response := runChannelHandler(t, SearchChannels, "/api/channel/search?keyword=tagsearch&tag_mode=1&status=enabled&p=1&page_size=50")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data struct {
			Items      []model.Channel  `json:"items"`
			Total      int64            `json:"total"`
			TypeCounts map[string]int64 `json:"type_counts"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	assert.EqualValues(t, 2, body.Data.Total, "the expanded tag set filtered to enabled rows is the reported total")
	assert.Len(t, body.Data.Items, 2)
	assert.EqualValues(t, 2, body.Data.TypeCounts["1"])
	assert.NotContains(t, body.Data.TypeCounts, "5", "the disabled tag member must not appear under the enabled filter")
}

// TestGetChannelOptionsPagesAndHydratesSelectedIds pins the bounded dropdown
// contract: the page is capped, an explicit page size is honored, keyword
// filtering reports total/has_more, an off-page selection is hydrated, the
// hydration list is deduplicated and capped, and no key is ever projected.
func TestGetChannelOptionsPagesAndHydratesSelectedIds(t *testing.T) {
	setupChannelCreateTest(t)
	created := make([]*model.Channel, 0, 5)
	for i := range 5 {
		created = append(created, insertListedChannel(t, "options-page-"+strconv.Itoa(i), 1, "gpt-4o, gpt-4o-mini"))
	}
	oldest := created[0]

	type optionsBody struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct {
				Id     int      `json:"id"`
				Name   string   `json:"name"`
				Models []string `json:"models"`
			} `json:"items"`
			Page     int   `json:"page"`
			PageSize int   `json:"page_size"`
			Total    int64 `json:"total"`
			HasMore  bool  `json:"has_more"`
		} `json:"data"`
	}
	read := func(target string) optionsBody {
		response := runChannelHandler(t, GetChannelOptions, target)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "sk-options-page-", "the options projection must never expose keys")
		var body optionsBody
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		require.True(t, body.Success, response.Body.String())
		return body
	}

	page := read("/api/channel/options?p=1&page_size=2")
	assert.Len(t, page.Data.Items, 2, "an explicit page_size must bound the result")
	assert.Equal(t, 1, page.Data.Page)
	assert.Equal(t, 2, page.Data.PageSize)
	assert.Equal(t, created[4].Id, page.Data.Items[0].Id, "results stay ordered by id desc")
	assert.EqualValues(t, 5, page.Data.Total)
	assert.True(t, page.Data.HasMore, "a partial page must report more rows")

	last := read("/api/channel/options?p=3&page_size=2")
	assert.False(t, last.Data.HasMore, "the final page must report no more rows")

	capped := read("/api/channel/options?page_size=100000")
	assert.Equal(t, 100, capped.Data.PageSize, "an oversized page_size must be capped server-side")

	keyword := read("/api/channel/options?keyword=options-page-3")
	require.Len(t, keyword.Data.Items, 1, "keyword must narrow the projection")
	assert.Equal(t, "options-page-3", keyword.Data.Items[0].Name)
	assert.EqualValues(t, 1, keyword.Data.Total)
	assert.False(t, keyword.Data.HasMore)

	// The oldest channel is on a later page; requesting it by id must still
	// resolve it without widening the query to the whole table.
	hydrated := read("/api/channel/options?p=1&page_size=2&ids=" + strconv.Itoa(oldest.Id))
	require.Len(t, hydrated.Data.Items, 3, "the requested id is appended to the page")
	assert.Equal(t, oldest.Id, hydrated.Data.Items[2].Id)
	assert.Equal(t, "options-page-0", hydrated.Data.Items[2].Name)
	assert.Equal(t, []string{"gpt-4o", "gpt-4o-mini"}, hydrated.Data.Items[2].Models)

	// A duplicated id list must not duplicate rows, and a long list must be capped
	// rather than turning the hydration into an unbounded IN query.
	repeated := "/api/channel/options?p=1&page_size=2&ids=" +
		strconv.Itoa(oldest.Id) + "," + strconv.Itoa(oldest.Id) + "," + strconv.Itoa(oldest.Id)
	deduped := read(repeated)
	assert.Len(t, deduped.Data.Items, 3, "duplicate requested ids must hydrate one row")

	longIds := make([]string, 0, 400)
	for i := range 400 {
		longIds = append(longIds, strconv.Itoa(100000+i))
	}
	bounded := read("/api/channel/options?p=1&page_size=2&ids=" + strings.Join(longIds, ","))
	assert.Len(t, bounded.Data.Items, 2, "an over-long id list of nonexistent ids must not add rows")
}

// TestGetChannelOptionsHydrationIsCappedWithRealChannels pins the hydration bound
// with real rows: asking for more ids than the cap may return at most page+cap
// items even when every requested id exists. A nonexistent-id test cannot prove
// this, because nothing is there to return.
func TestGetChannelOptionsHydrationIsCappedWithRealChannels(t *testing.T) {
	setupChannelCreateTest(t)
	const total = 140
	created := make([]*model.Channel, 0, total)
	for i := range total {
		created = append(created, insertListedChannel(t, "options-hydrate-"+strconv.Itoa(i), 1, "gpt-4o"))
	}

	// Request every id, oldest first, so the page (id desc) cannot cover them.
	ids := make([]string, 0, total)
	for _, channel := range created {
		ids = append(ids, strconv.Itoa(channel.Id))
	}

	response := runChannelHandler(t, GetChannelOptions,
		"/api/channel/options?p=1&page_size=2&ids="+strings.Join(ids, ","))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data struct {
			Items   []struct{ Id int } `json:"items"`
			Total   int64              `json:"total"`
			HasMore bool               `json:"has_more"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	assert.EqualValues(t, total, body.Data.Total)
	// 2 page rows + at most 100 hydrated extras, never the full requested list.
	assert.Len(t, body.Data.Items, 102, "hydration must be capped at 100 real rows on top of the page")
}

// TestGetChannelOptionsHasMoreIgnoresHydration pins that hydrated off-page ids
// never make a partial page look final: page 1 of a 57-row table with a full
// hydration list must still report has_more.
func TestGetChannelOptionsHasMoreIgnoresHydration(t *testing.T) {
	setupChannelCreateTest(t)
	const total = 57
	created := make([]*model.Channel, 0, total)
	for i := range total {
		created = append(created, insertListedChannel(t, "options-has-more-"+strconv.Itoa(i), 1, "gpt-4o"))
	}
	// Request the oldest ids by id, which page 1 (id desc, 30 rows) cannot cover:
	// they are exactly the off-page rows the hydration branch must append.
	ids := make([]string, 0, total-30)
	for _, channel := range created[:total-30] {
		ids = append(ids, strconv.Itoa(channel.Id))
	}

	read := func(target string) (int, int64, bool) {
		response := runChannelHandler(t, GetChannelOptions, target)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data struct {
				Items   []struct{ Id int } `json:"items"`
				Total   int64              `json:"total"`
				HasMore bool               `json:"has_more"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		return len(body.Data.Items), body.Data.Total, body.Data.HasMore
	}

	items, matched, hasMore := read("/api/channel/options?p=1&page_size=30&ids=" + strings.Join(ids, ","))
	assert.EqualValues(t, total, matched)
	assert.Greater(t, items, 30, "the hydrated ids must be appended to the page")
	assert.True(t, hasMore, "hydration must not make page 1 look like the last page")

	items, _, hasMore = read("/api/channel/options?p=2&page_size=30")
	assert.Equal(t, 27, items)
	assert.False(t, hasMore, "the final page reports no more rows")
}

// TestGetChannelOptionsRejectsOverflowingPageOffset pins that a huge page number
// cannot overflow the offset arithmetic into a negative or wrapped OFFSET.
func TestGetChannelOptionsRejectsOverflowingPageOffset(t *testing.T) {
	setupChannelCreateTest(t)
	insertListedChannel(t, "options-overflow", 1, "gpt-4o")

	response := runChannelHandler(t, GetChannelOptions, "/api/channel/options?p=2147483647&page_size=100")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct{ Id int } `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	assert.True(t, body.Success, "a huge page must return an empty page, not fail")
	assert.Empty(t, body.Data.Items, "an out-of-range page must not wrap back to the first page")
}
