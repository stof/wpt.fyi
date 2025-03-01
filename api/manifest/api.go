// Copyright 2018 The WPT Dashboard Project. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

//go:generate mockgen -destination mock_manifest/api_mock.go github.com/web-platform-tests/wpt.fyi/api/manifest API

package manifest

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"regexp"
	"time"

	"github.com/google/go-github/v47/github"
	"github.com/web-platform-tests/wpt.fyi/shared"
)

// AssetRegex is the pattern for a valid manifest filename.
// The full sha is captured in group 1.
var AssetRegex = regexp.MustCompile(`^MANIFEST-([0-9a-fA-F]{40}).json.gz$`)

// API handles manifest-related fetches and caching.
type API interface {
	GetManifestForSHA(string) (string, []byte, error)
	NewRedis(duration time.Duration) shared.ReadWritable
}

type apiImpl struct {
	ctx context.Context
}

// NewAPI returns an API implementation for the given context.
func NewAPI(ctx context.Context) API {
	return apiImpl{
		ctx: ctx,
	}
}

// GetManifestForSHA loads the (gzipped) contents of the manifest JSON for the release associated
// with the given SHA, if any.
func (a apiImpl) GetManifestForSHA(sha string) (fetchedSHA string, manifest []byte, err error) {
	aeAPI := shared.NewAppEngineAPI(a.ctx)
	fetchedSHA, body, err := getGitHubReleaseAssetForSHA(aeAPI, sha)
	if err != nil {
		return fetchedSHA, nil, err
	}
	data, err := ioutil.ReadAll(body)
	if err != nil {
		return fetchedSHA, nil, err
	}
	return fetchedSHA, data, err
}

// getGitHubReleaseAssetForSHA gets the bytes for the SHA's release's manifest json gzip asset.
// This is done using a few hops on the GitHub API, so should be cached afterward.
func getGitHubReleaseAssetForSHA(aeAPI shared.AppEngineAPI, sha string) (fetchedSHA string, manifest io.Reader, err error) {
	client, err := aeAPI.GetGitHubClient()
	if err != nil {
		return "", nil, err
	}
	var release *github.RepositoryRelease
	releaseTag := "latest"
	if shared.IsLatest(sha) {
		// Use GitHub's API for latest release.
		release, _, err = client.Repositories.GetLatestRelease(aeAPI.Context(), shared.WPTRepoOwner, shared.WPTRepoName)
	} else {
		q := fmt.Sprintf("SHA:%s repo:web-platform-tests/wpt", sha)
		issues, _, err := client.Search.Issues(aeAPI.Context(), q, nil)
		if err != nil {
			return "", nil, err
		}
		if issues == nil || len(issues.Issues) < 1 {
			return "", nil, fmt.Errorf("No search results found for SHA %s", sha)
		}

		releaseTag = fmt.Sprintf("merge_pr_%d", issues.Issues[0].GetNumber())
		release, _, err = client.Repositories.GetReleaseByTag(aeAPI.Context(), shared.WPTRepoOwner, shared.WPTRepoName, releaseTag)
	}

	if err != nil {
		return "", nil, err
	} else if release == nil || len(release.Assets) < 1 {
		return "", nil, fmt.Errorf("No assets found for %s release", releaseTag)
	}
	// Get (and unzip) the asset with name "MANIFEST-{sha}.json.gz"
	for _, asset := range release.Assets {
		name := asset.GetName()
		var url string
		if matches := AssetRegex.FindStringSubmatch(name); matches != nil {
			fetchedSHA = matches[1]
			url = asset.GetBrowserDownloadURL()

			client := aeAPI.GetHTTPClient()
			resp, err := client.Get(url)
			if err != nil {
				return fetchedSHA, nil, err
			}
			return fetchedSHA, resp.Body, err
		}
	}
	return "", nil, fmt.Errorf("No manifest asset found for release %s", releaseTag)
}

// NewRedis creates a new redisReadWritable with the given duration.
func (a apiImpl) NewRedis(duration time.Duration) shared.ReadWritable {
	return shared.NewRedisReadWritable(a.ctx, duration)
}
