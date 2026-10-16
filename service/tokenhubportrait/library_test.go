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
	mu                          sync.Mutex
	assets                      map[string]map[string]any
	uploads, requests, sessions int
	status                      string
	loseCreate, rejectCreate    bool
	projects                    []string
}

func (u *portraitUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests++
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
	case r.URL.Path == "/v1/portrait/session":
		u.sessions++
		result = map[string]any{"BytedToken": fmt.Sprintf("liveness-%d", u.sessions), "H5Link": "https://h5.example.com/one-time-link"}
	case r.URL.Path == "/v1/portrait/validate-result":
		result = map[string]any{"GroupId": "group-real"}
	case strings.HasPrefix(r.URL.Path, "/v1/portrait/asset-group/"):
		result = map[string]any{"Id": "group-real", "GroupType": "LivenessFace", "Status": "active"}
	case r.URL.Path == "/v1/portrait/asset-group":
		result = map[string]any{"Id": "group-virtual", "GroupType": "AIGC"}
	case r.URL.Path == "/v1/portrait/asset" && r.Method == http.MethodPost:
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
		items := []any{}
		for _, asset := range u.assets {
			items = append(items, asset)
		}
		result = map[string]any{"Items": items, "Total": len(items)}
	case strings.HasPrefix(r.URL.Path, "/v1/portrait/asset/"):
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
	require.NoError(t, db.AutoMigrate(&model.TokenHubPortraitSession{}, &model.TokenHubPortraitGroup{}, &model.TokenHubPortraitAsset{}))
	upstream := &portraitUpstream{assets: map[string]map[string]any{}, status: "Active"}
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

func authenticate(t *testing.T, c *Client) string {
	t.Helper()
	ctx := context.Background()
	result, err := c.CreateSession(ctx, "")
	require.NoError(t, err)
	result, err = c.ValidateSession(ctx, String(result, "BytedToken"))
	require.NoError(t, err)
	return String(result, "GroupId")
}

func TestPortraitLifecycleAndReferenceReplacement(t *testing.T) {
	c, _, up := setup(t)
	group := authenticate(t, c)
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
	group := authenticate(t, c)
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

func TestPortraitAuthenticationAndSessionExpiry(t *testing.T) {
	c, _, up := setup(t)
	_, err := c.OwnGroup(context.Background(), "")
	require.Equal(t, "portrait_authentication_required", PublicError(err).Code)
	_, err = c.ValidateSession(context.Background(), "not-created-by-caller")
	require.Equal(t, "portrait_session_not_found", PublicError(err).Code)
	require.Zero(t, up.requests)
	session, err := c.CreateSession(context.Background(), "")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.TokenHubPortraitSession{}).Where("scope = ?", c.Scope).Update("expires_at", time.Now().Unix()-1).Error)
	_, err = c.ValidateSession(context.Background(), String(session, "BytedToken"))
	require.Equal(t, "portrait_session_expired", PublicError(err).Code)
	require.Equal(t, 1, up.requests)
}

func TestPortraitConcurrentUploadCreatesOnlyOneAsset(t *testing.T) {
	c, _, up := setup(t)
	group := authenticate(t, c)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
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
	group := authenticate(t, c)
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
	group := authenticate(t, c)
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
	group := authenticate(t, c)
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
	group := authenticate(t, c)
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
	require.NoError(t, c.allow(ctx, "POST", "session"))
	require.NoError(t, other.allow(ctx, "POST", "session"))
	require.NoError(t, c.allow(ctx, "POST", "session"))
	require.Equal(t, "portrait_rate_limited", PublicError(other.allow(ctx, "POST", "session")).Code)
	// Validate-result has its own upstream action limit.
	require.NoError(t, c.allow(ctx, "POST", "validate-result"))
}
