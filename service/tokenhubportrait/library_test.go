package tokenhubportrait

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type portraitUpstream struct {
	mu                              sync.Mutex
	assets                          map[string]map[string]any
	uploads, requests, groupCreates int
	groups                          map[string]map[string]any
	loseGroupCreate                 bool
	activateAfter                   int
	assetGets                       int
	events                          []string
	status                          string
	loseCreate, rejectCreate        bool
	projects                        []string
}

func (u *portraitUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests++
	u.events = append(u.events, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer upstream-test-key" {
		http.Error(w, "bad auth", 401)
		return
	}
	var body map[string]any
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodGet || r.Method == http.MethodDelete {
		if err := common.DecodeJson(r.Body, &body); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		u.projects = append(u.projects, String(body, "ProjectName"))
	} else {
		u.projects = append(u.projects, r.URL.Query().Get("ProjectName"))
	}
	if r.Method == http.MethodGet && String(body, "Id") != strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/v1/portrait/asset-group/"), "/v1/portrait/asset/") {
		http.Error(w, "Id missing or mismatched", 400)
		return
	}
	var result any
	switch {
	case r.URL.Path == "/v1/portrait/asset-group/list":
		items := []any{}
		for _, group := range u.groups {
			items = append(items, group)
		}
		result = map[string]any{"Items": items}
	case strings.HasPrefix(r.URL.Path, "/v1/portrait/asset-group/"):
		result = u.groups[strings.TrimPrefix(r.URL.Path, "/v1/portrait/asset-group/")]
		if result == nil {
			http.NotFound(w, r)
			return
		}
	case r.URL.Path == "/v1/portrait/asset-group":
		u.groupCreates++
		id := fmt.Sprintf("group-virtual-%d", u.groupCreates)
		u.groups[id] = map[string]any{"Id": id, "GroupType": body["GroupType"], "Name": body["Name"], "ProjectName": body["ProjectName"]}
		if u.loseGroupCreate {
			u.loseGroupCreate = false
			panic(http.ErrAbortHandler)
		}
		result = map[string]any{"Id": id, "GroupType": "AIGC"}
	case r.URL.Path == "/v1/portrait/asset" && r.Method == http.MethodPost:
		if utf8.RuneCountInString(String(body, "Name")) > 64 {
			http.Error(w, "Name exceeds 64 characters", 400)
			return
		}
		u.uploads++
		if u.rejectCreate {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			raw, _ := common.Marshal(map[string]any{"ResponseMetadata": map[string]any{"Error": map[string]any{"Code": "InvalidParameter", "Message": "secret signed URL must not escape"}}})
			w.Write(raw)
			return
		}
		id := fmt.Sprintf("asset-%d", u.uploads)
		u.assets[id] = map[string]any{"Id": id, "GroupId": body["GroupId"], "AssetType": body["AssetType"], "Name": body["Name"], "Status": u.status, "URL": "https://media.example.com/retained"}
		if u.loseCreate {
			u.loseCreate = false
			panic(http.ErrAbortHandler)
		}
		result = map[string]any{"Id": id}
	case r.URL.Path == "/v1/portrait/asset/list":
		filter, _ := body["Filter"].(map[string]any)
		if String(filter, "GroupType") != "AIGC" {
			http.Error(w, "Filter.GroupType required", 400)
			return
		}
		items := []any{}
		for _, asset := range u.assets {
			items = append(items, asset)
		}
		result = map[string]any{"Items": items, "Total": len(items)}
	case strings.HasPrefix(r.URL.Path, "/v1/portrait/asset/"):
		u.assetGets++
		if u.activateAfter > 0 && u.assetGets >= u.activateAfter {
			for _, asset := range u.assets {
				asset["Status"] = "Active"
			}
		}
		result = u.assets[strings.TrimPrefix(r.URL.Path, "/v1/portrait/asset/")]
		if result == nil {
			http.NotFound(w, r)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	raw, err := common.Marshal(map[string]any{"ResponseMetadata": map[string]any{}, "Result": result})
	if err != nil {
		panic(err)
	}
	w.Write(raw)
}

func setup(t *testing.T) (*Client, *model.Channel, *portraitUpstream) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	previous := model.DB
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	model.DB = db
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { model.DB = previous; common.RedisEnabled = previousRedis; sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.TokenHubPortraitGroup{}, &model.TokenHubPortraitAsset{}))
	upstream := &portraitUpstream{assets: map[string]map[string]any{}, groups: map[string]map[string]any{}, status: "Active"}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	base := server.URL
	ch := &model.Channel{Id: 128, Type: constant.ChannelTypeSora, Key: "upstream-test-key", BaseURL: &base}
	ch.SetOtherSettings(dto.ChannelOtherSettings{TokenHubPortraitEnabled: true})
	client, err := NewClient(ch, 1, 7, "")
	require.NoError(t, err)
	client.SourceDigest = func(ctx context.Context, source string) (string, error) {
		// Signed URLs for the same content have the same digest; different content
		// gets a different digest even when the URL path has not changed.
		if strings.Contains(source, "second-content") {
			return Digest("second-content"), nil
		}
		return Digest("same-content"), nil
	}
	return client, ch, upstream
}

func virtualGroup(t *testing.T, c *Client) string {
	t.Helper()
	result, err := c.CreateGroup(context.Background(), "虚拟人像测试")
	require.NoError(t, err)
	return String(result, "Id")
}

func TestPortraitProjectUsesBusinessDefaultOrExplicitConfiguration(t *testing.T) {
	c, ch, up := setup(t)
	require.Equal(t, "深圳赞卫数字科技有限公司", c.Project)
	settings := ch.GetOtherSettings()
	settings.TokenHubPortraitProject = "provider-confirmed-project"
	ch.SetOtherSettings(settings)
	configured, err := NewClient(ch, 1, 7, "")
	require.NoError(t, err)
	require.Equal(t, settings.TokenHubPortraitProject, configured.Project)
	require.NotEqual(t, c.Scope, configured.Scope)
	group := virtualGroup(t, configured)
	_, err = configured.Group(context.Background(), group)
	require.NoError(t, err)
	up.mu.Lock()
	defer up.mu.Unlock()
	for _, project := range up.projects {
		require.Equal(t, settings.TokenHubPortraitProject, project)
	}
}

func TestPortraitLifecycleAndReferenceReplacement(t *testing.T) {
	c, _, up := setup(t)
	group := virtualGroup(t, c)
	body := map[string]any{"model": "doubao-seedance-2-0-260128", "images": []any{"https://files.example/a?sig=first"}, "metadata": map[string]any{
		"portrait_group_id": group, "duration": 15, "generate_audio": false, "seed": 0,
		"content": []any{map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "https://files.example/a?sig=first"}}, map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "https://files.example/b"}}},
	}}
	require.NoError(t, c.PrepareVideo(context.Background(), body))
	metadata := body["metadata"].(map[string]any)
	require.NotContains(t, metadata, "portrait_group_id")
	require.Equal(t, false, metadata["generate_audio"])
	require.Equal(t, 0, metadata["seed"])
	require.Equal(t, 15, metadata["duration"])
	items := metadata["content"].([]any)
	image := items[0].(map[string]any)
	video := items[1].(map[string]any)
	require.Equal(t, "first_frame", image["role"])
	require.Equal(t, "reference_video", video["role"])
	require.Equal(t, "asset://asset-1", image["image_url"].(map[string]any)["url"])
	require.Equal(t, "asset://asset-1", body["images"].([]any)[0])
	require.Equal(t, "asset://asset-2", video["video_url"].(map[string]any)["url"])
	require.Equal(t, 2, up.uploads)
	for _, project := range up.projects {
		require.Equal(t, DefaultProject, project)
	}
	// Refresh signatures reuse content, but changed content cannot alias it.
	asset, err := c.EnsureAsset(context.Background(), group, "https://files.example/a?sig=second", "Image", 0)
	require.NoError(t, err)
	require.Equal(t, "asset-1", asset.AssetId)
	require.Equal(t, 2, up.uploads)
	_, err = c.EnsureAsset(context.Background(), group, "https://files.example/a?sig=second-content", "Image", 0)
	require.NoError(t, err)
	require.Equal(t, 3, up.uploads)
}

func TestPortraitOwnershipIncludesUserTokenChannelCredentialAndProject(t *testing.T) {
	c, ch, up := setup(t)
	group := virtualGroup(t, c)
	_, err := c.EnsureAsset(context.Background(), group, "https://files.example/video", "Video", 0)
	require.NoError(t, err)
	before := up.requests
	for _, change := range []func(*model.Channel) (int, int, string){
		func(*model.Channel) (int, int, string) { return 2, 7, "" },
		func(*model.Channel) (int, int, string) { return 1, 8, "" },
		func(ch *model.Channel) (int, int, string) { ch.Id++; return 1, 7, "" },
		func(*model.Channel) (int, int, string) { return 1, 7, "rotated-upstream-key" },
		func(ch *model.Channel) (int, int, string) {
			s := ch.GetOtherSettings()
			s.TokenHubPortraitProject = "other-project"
			ch.SetOtherSettings(s)
			return 1, 7, ""
		},
	} {
		copy := *ch
		user, token, key := change(&copy)
		other, e := NewClient(&copy, user, token, key)
		require.NoError(t, e)
		_, e = other.EnsureAsset(context.Background(), group, "asset://asset-1", "Video", 0)
		require.Equal(t, "portrait_group_not_found", PublicError(e).Code)
	}
	require.Equal(t, before, up.requests, "unauthorized group must be blocked before upstream calls")
}

func TestPortraitRejectsNonVirtualGroupsBeforeUpstreamCalls(t *testing.T) {
	c, _, up := setup(t)
	_, err := c.OwnGroup(context.Background(), "")
	require.Equal(t, "portrait_group_selection_required", PublicError(err).Code)
	require.NoError(t, model.DB.Create(&model.TokenHubPortraitGroup{BindingKey: "old-real-group", Scope: c.Scope, GroupId: "real-group", GroupType: "LivenessFace"}).Error)
	_, err = c.EnsureAsset(context.Background(), "real-group", "https://files.example/video", "Video", 0)
	require.Equal(t, "portrait_virtual_only", PublicError(err).Code)
	require.Zero(t, up.requests)
}

func TestPortraitGroupOptionalStatusAndProjectIsolation(t *testing.T) {
	for _, scenario := range []string{"no-status", "explicit-inactive", "wrong-project"} {
		t.Run(scenario, func(t *testing.T) {
			c, _, up := setup(t)
			group := virtualGroup(t, c)
			up.mu.Lock()
			switch scenario {
			case "explicit-inactive":
				up.groups[group]["Status"] = "disabled"
			case "wrong-project":
				up.groups[group]["ProjectName"] = "foreign-project"
			}
			up.mu.Unlock()
			_, err := c.Group(context.Background(), group)
			switch scenario {
			case "no-status":
				require.NoError(t, err)
			case "explicit-inactive":
				require.Equal(t, "portrait_group_not_active", PublicError(err).Code)
			case "wrong-project":
				require.Equal(t, "portrait_group_identity_mismatch", PublicError(err).Code)
			}
		})
	}
}

func TestPortraitConcurrentUploadCreatesOnlyOneAsset(t *testing.T) {
	c, _, up := setup(t)
	group := virtualGroup(t, c)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.EnsureAsset(context.Background(), group, "https://files.example/video", "Video", 0)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			require.Equal(t, "portrait_asset_processing", PublicError(err).Code)
		}
	}
	require.Equal(t, 1, up.uploads)
}

func TestPortraitLostCreateResponseReconcilesWithoutReupload(t *testing.T) {
	c, _, up := setup(t)
	group := virtualGroup(t, c)
	up.mu.Lock()
	up.loseCreate = true
	up.mu.Unlock()
	_, err := c.EnsureAsset(context.Background(), group, "https://files.example/video", "Video", 0)
	require.Equal(t, "portrait_asset_submission_unknown", PublicError(err).Code)
	// A fresh client represents a restarted process; recovery relies on the DB.
	fresh := *c
	asset, err := fresh.EnsureAsset(context.Background(), group, "https://files.example/video?new-signature", "Video", 0)
	require.NoError(t, err)
	require.Equal(t, "asset-1", asset.AssetId)
	require.Equal(t, 1, up.uploads)
}

func TestPortraitUnknownCreateDoesNotRetryWhenNotFound(t *testing.T) {
	c, _, up := setup(t)
	group := virtualGroup(t, c)
	up.mu.Lock()
	up.loseCreate = true
	up.mu.Unlock()
	_, err := c.EnsureAsset(context.Background(), group, "https://files.example/video", "Video", 0)
	require.Error(t, err)
	up.mu.Lock()
	up.assets = map[string]map[string]any{}
	up.mu.Unlock()
	_, err = c.EnsureAsset(context.Background(), group, "https://files.example/video", "Video", 0)
	require.Equal(t, "portrait_asset_submission_unknown", PublicError(err).Code)
	require.Equal(t, 1, up.uploads)
}

func TestPortraitProcessingFailureAndRevocationBlockGeneration(t *testing.T) {
	c, _, up := setup(t)
	group := virtualGroup(t, c)
	up.mu.Lock()
	up.status = "Processing"
	up.mu.Unlock()
	asset, err := c.EnsureAsset(context.Background(), group, "https://files.example/video", "Video", 0)
	require.NoError(t, err)
	require.Equal(t, "Processing", asset.Status)
	up.mu.Lock()
	up.assets[asset.AssetId]["Status"] = "Failed"
	up.mu.Unlock()
	_, err = c.EnsureAsset(context.Background(), group, "asset://"+asset.AssetId, "Video", 0)
	require.Equal(t, "portrait_asset_failed", PublicError(err).Code)
	require.NoError(t, model.DB.Model(&model.TokenHubPortraitGroup{}).Where("scope = ?", c.Scope).Update("revoked", true).Error)
	_, err = c.EnsureAsset(context.Background(), group, "asset://"+asset.AssetId, "Video", 0)
	require.Equal(t, "portrait_group_not_found", PublicError(err).Code)
}

func TestPortraitDefiniteUploadErrorIsNotMisreportedAsAmbiguous(t *testing.T) {
	c, _, up := setup(t)
	group := virtualGroup(t, c)
	up.mu.Lock()
	up.rejectCreate = true
	up.mu.Unlock()
	_, err := c.EnsureAsset(context.Background(), group, "https://files.example/video", "Video", 0)
	require.Equal(t, "portrait_upstream_InvalidParameter", PublicError(err).Code)
	require.NotContains(t, err.Error(), "secret")
	_, err = c.EnsureAsset(context.Background(), group, "https://files.example/video", "Video", 0)
	require.Equal(t, "portrait_upstream_InvalidParameter", PublicError(err).Code)
	require.Equal(t, 1, up.uploads)
}

func TestPortraitTextOnlyAndInvalidReferencesDoNotUpload(t *testing.T) {
	c, _, up := setup(t)
	require.NoError(t, c.PrepareVideo(context.Background(), map[string]any{"prompt": "hello"}))
	err := c.PrepareVideo(context.Background(), map[string]any{"metadata": map[string]any{"content": []any{map[string]any{"video_url": 123}}}})
	require.Equal(t, "portrait_reference_invalid", PublicError(err).Code)
	require.Zero(t, up.uploads)
}

func TestSourceURLRejectsPrivateAndNonHTTP(t *testing.T) {
	for _, source := range []string{"file:///etc/passwd", "http://127.0.0.1/video", "http://169.254.169.254/latest/meta-data", "http://[::1]/video"} {
		_, err := hashSource(context.Background(), source)
		require.Equal(t, "portrait_source_url_invalid", PublicError(err).Code)
	}
}

func TestPortraitRateLimitSharedAcrossCallerScopes(t *testing.T) {
	c, ch, _ := setup(t)
	other, err := NewClient(ch, 2, 99, "")
	require.NoError(t, err)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		require.NoError(t, c.allow(ctx, "POST", "asset/list"))
	}
	require.Equal(t, "portrait_rate_limited", PublicError(other.allow(ctx, "POST", "asset/list")).Code)
	require.NoError(t, c.allow(ctx, "GET", "asset/id"))
}

func TestPortraitAutomaticallyUploadsAndWaitsForActive(t *testing.T) {
	c, _, up := setup(t)
	up.mu.Lock()
	up.status = "Processing"
	up.activateAfter = 2
	up.mu.Unlock()
	body := map[string]any{"model": "doubao-seedance-2-0-260128", "prompt": "虚拟人物口播", "metadata": map[string]any{"content": []any{map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "https://files.example/virtual.png"}}}}}
	require.NoError(t, c.PrepareVideo(context.Background(), body))
	metadata := body["metadata"].(map[string]any)
	item := metadata["content"].([]any)[0].(map[string]any)
	require.Equal(t, "asset://asset-1", item["image_url"].(map[string]any)["url"])
	up.mu.Lock()
	require.Equal(t, "Active", up.assets["asset-1"]["Status"])
	events := append([]string(nil), up.events...)
	up.mu.Unlock()
	require.Equal(t, "POST /v1/portrait/asset-group", events[0])
	require.Equal(t, "POST /v1/portrait/asset", events[2])
	require.Equal(t, "GET /v1/portrait/asset/asset-1", events[len(events)-1])
	// A new client represents a later request/process: the group and upload are reused.
	fresh := *c
	_, err := fresh.EnsureAsset(context.Background(), "", "https://files.example/virtual.png?renewed", "Image", 0)
	require.NoError(t, err)
	up.mu.Lock()
	defer up.mu.Unlock()
	require.Equal(t, 1, up.groupCreates)
	require.Equal(t, 1, up.uploads)
}

func TestPortraitProcessingTimeoutRetainsAssetForNextRequest(t *testing.T) {
	c, _, up := setup(t)
	up.mu.Lock()
	up.status = "Processing"
	up.mu.Unlock()
	_, err := c.EnsureAsset(context.Background(), "", "https://files.example/virtual.png", "Image", time.Nanosecond)
	require.Equal(t, "portrait_asset_processing", PublicError(err).Code)
	var saved model.TokenHubPortraitAsset
	require.NoError(t, model.DB.Where("scope = ?", c.Scope).First(&saved).Error)
	require.Equal(t, "asset-1", saved.AssetId)
	require.Equal(t, "Processing", saved.Status)
	up.mu.Lock()
	up.assets[saved.AssetId]["Status"] = "Active"
	up.mu.Unlock()
	// The later request resumes the same upload after upstream processing completes.
	fresh := *c
	asset, err := fresh.EnsureAsset(context.Background(), "", "https://files.example/virtual.png?renewed", "Image", time.Second)
	require.NoError(t, err)
	require.Equal(t, saved.AssetId, asset.AssetId)
	require.Equal(t, "Active", asset.Status)
	up.mu.Lock()
	defer up.mu.Unlock()
	require.Equal(t, 1, up.groupCreates)
	require.Equal(t, 1, up.uploads)
}

func TestPortraitDefaultGroupLostResponseRecoversWithoutRecreating(t *testing.T) {
	c, _, up := setup(t)
	up.mu.Lock()
	up.loseGroupCreate = true
	up.mu.Unlock()
	_, err := c.DefaultVirtualGroup(context.Background())
	require.Equal(t, "portrait_group_submission_unknown", PublicError(err).Code)
	fresh := *c
	group, err := fresh.DefaultVirtualGroup(context.Background())
	require.NoError(t, err)
	require.Equal(t, "group-virtual-1", group)
	up.mu.Lock()
	defer up.mu.Unlock()
	require.Equal(t, 1, up.groupCreates)
}

func TestPortraitDefaultGroupUnknownOutcomeNeverBlindlyRecreates(t *testing.T) {
	c, _, up := setup(t)
	up.mu.Lock()
	up.loseGroupCreate = true
	up.mu.Unlock()
	_, err := c.DefaultVirtualGroup(context.Background())
	require.Error(t, err)
	up.mu.Lock()
	up.groups = map[string]map[string]any{}
	up.mu.Unlock()
	_, err = c.DefaultVirtualGroup(context.Background())
	require.Equal(t, "portrait_group_submission_unknown", PublicError(err).Code)
	up.mu.Lock()
	defer up.mu.Unlock()
	require.Equal(t, 1, up.groupCreates)
}

func TestPortraitConcurrentDefaultLibraryCreatesOneGroup(t *testing.T) {
	c, _, up := setup(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.EnsureAsset(context.Background(), "", "https://files.example/virtual.png", "Image", 0)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			require.Contains(t, []string{"portrait_group_processing", "portrait_asset_processing"}, PublicError(err).Code)
		}
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	require.Equal(t, 1, up.groupCreates)
	require.Equal(t, 1, up.uploads)
}
