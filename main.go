package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gofrs/flock"
	cfg "github.com/golobby/config/v3"
	"github.com/golobby/config/v3/pkg/feeder"
	log "github.com/sirupsen/logrus"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"
)

// redirectURI is the OAuth redirect URI for the application.
// You must register an application at Spotify's developer portal
// and enter this value.
const redirectURI = "http://localhost:8080/callback"

var (
	config        = Config{}
	auth          = &spotifyauth.Authenticator{}
	ch            = make(chan *spotify.Client)
	rch           = make(chan string)
	state         = "spotifyLiked"
	authfile      = ".spotify_auth"
	loginPageHTML = `
	<!DOCTYPE html>
	<html lang="en">
	<head>
	<script>
		window.addEventListener('load', function() {
			window.close();
		});
	</script>
	<meta http-equiv="Content-Type" content="text/html; charset=utf-8"/>
	<title>
	Login successful
	</title>
	</head>
	<body>
	Login successful
	</body>
	</html>
	`
)

type Config struct {
	ID       string `env:"SPOTIFY_ID"`
	Secret   string `env:"SPOTIFY_SECRET"`
	LogLevel string `env:"LOGLEVEL"`
}

// Set up Config, logging
func init() {
	// Read from .env and override from the local environment
	dotEnvFeeder := feeder.DotEnv{Path: ".env"}
	envFeeder := feeder.Env{}

	_ = cfg.New().AddFeeder(dotEnvFeeder).AddStruct(&config).Feed()
	_ = cfg.New().AddFeeder(envFeeder).AddStruct(&config).Feed()

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	loglevel := strings.ToLower(config.LogLevel)
	switch loglevel {
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}
}

func main() {
	log.Debug("creating new spotify client")
	auth = newSpotifyAuthClient()

	registerHTTPPaths()

	readAuthFile()

	// See if saved auth is still good (async, coordinated with channels)
	go trySavedAuth()

	// this waits for the client to be provided in the channel
	// and then handles the main functionality (getting true/false and sending
	// through rch channel)
	go findOutIfTrackIsLiked()

	// Either the saved auth or new auth will send the result through the channel
	result := <-rch
	log.Debug("received channel response")
	// This is not returned via log package because it should be the raw value
	fmt.Print(result)
}

func registerHTTPPaths() {
	http.HandleFunc("/callback", completeAuth)
	http.HandleFunc("/favicon", faviconHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Info("Got request for:", r.URL.String())
	})
}

func newSpotifyAuthClient() *spotifyauth.Authenticator {
	return spotifyauth.New(spotifyauth.WithRedirectURL(redirectURI),
		spotifyauth.WithClientID(config.ID),
		spotifyauth.WithClientSecret(config.Secret),
		spotifyauth.WithScopes(spotifyauth.ScopeUserReadPlaybackState,
			spotifyauth.ScopeUserLibraryRead))
}

func readAuthFile() {
	log.Debug("getting authfile")
	file := os.Getenv("HOME") + "/" + authfile
	log.Debug("create flock")
	fileLock := flock.New(file)
	log.Debug("trying to unlock file")
	lock, err := fileLock.TryLock()
	if err != nil || !lock {
		log.Fatalf("%v: File is locked, exiting", authfile)
		os.Exit(1)
	}
	log.Debug("file lock succeeded")
	defer unlockAuthFile(fileLock)
}

func unlockAuthFile(fileLock *flock.Flock) {
	err := fileLock.Unlock()
	if err != nil {
		log.Warn("error unlocking file: %w", err)
	}
}

func trySavedAuth() {
	log.Info("trying saved auth")
	token := validateContext(context.Background())

	if token.AccessToken == "" {
		log.Warn("access token blank")
		go func() {
			log.Debug("starting http server")
			err := http.ListenAndServe(":8080", nil)
			if err != nil {
				log.Fatal("unable to start http server: %w", err)
			}
		}()

		url := auth.AuthURL(state)
		log.Debug("opening url to auth")
		err := exec.Command("open", url).Start()
		if err != nil {
			log.Errorf("error running command: %+v", err)
		}
	}
}

func findOutIfTrackIsLiked() {
	// wait for auth to complete (either saved auth or user-interacted auth)
	client := <-ch
	// write the token first
	token, err := client.Token()
	if err != nil {
		log.Warn("Error using token: %w", err)
		return
	}

	log.Debug("getting state")
	state, _ := client.PlayerCurrentlyPlaying(context.Background())
	if state.Item == nil {
		log.Warn("item was empty, maybe nothing playing?")
		log.Warn("Spotify's API sometimes returns no content (204) when apps on different devices are out of sync")
		log.Warn("https://community.spotify.com/t5/Spotify-for-Developers/204-regularly-being-incorrectly-returned-for-v1-me-player/m-p/5323282/highlight/true#M3879")
		log.Warn("try restarting spotify on all devices logged into this account")
		rch <- "false"
		return
	}
	currentItem := state.Item
	log.Debug("checking if track is liked")
	isSaved, err := client.UserHasTracks(context.Background(), currentItem.ID)
	if err != nil {
		log.Panicf("Error getting currently playing song: %+v", err)
	}
	log.Debug("writing new token")
	file, _ := json.MarshalIndent(token, "", " ")
	writeFile(authfile, file)
	log.Debug("sending response to channel")
	rch <- fmt.Sprintf("%t", isSaved[0])
}

func validateContext(ctx context.Context) *oauth2.Token {
	log.Debug("reading authfile")
	tokenFile := readFile(authfile)
	token := &oauth2.Token{}
	_ = json.Unmarshal([]byte(tokenFile), token)
	log.Debug("creating new spotify client")
	// use the token to get an authenticated client
	client := spotify.New(auth.Client(ctx, token))
	user, err := client.CurrentUser(context.Background())
	if user == nil {
		log.Warnf("Saved token didn't work: %+v", err)
		log.Warnf("Try opening https://spotify.com in your browser and re-running")
		return token
	}
	log.Debug("sending channel")
	ch <- client
	return token
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	log.Debug("creating new oauth token")
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}
	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		log.Fatalf("State mismatch: %s != %s\n", st, state)
	}

	// use the token to get an authenticated client
	log.Debug("getting new spotify auth client")
	client := spotify.New(auth.Client(r.Context(), tok))
	fmt.Fprint(w, loginPageHTML)
	ch <- client
}
