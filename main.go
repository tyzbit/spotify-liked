package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"

	cfg "github.com/golobby/config/v3"
	"github.com/golobby/config/v3/pkg/feeder"
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
	ID     string `env:"SPOTIFY_ID"`
	Secret string `env:"SPOTIFY_SECRET"`
}

// Set up Config
func init() {
	// Read from .env and override from the local environment
	dotEnvFeeder := feeder.DotEnv{Path: ".env"}
	envFeeder := feeder.Env{}

	_ = cfg.New().AddFeeder(dotEnvFeeder).AddStruct(&config).Feed()
	_ = cfg.New().AddFeeder(envFeeder).AddStruct(&config).Feed()
}

func main() {
	auth = spotifyauth.New(spotifyauth.WithRedirectURL(redirectURI),
		spotifyauth.WithClientID(config.ID),
		spotifyauth.WithClientSecret(config.Secret),
		spotifyauth.WithScopes(spotifyauth.ScopeUserReadPlaybackState,
			spotifyauth.ScopeUserLibraryRead))

	http.HandleFunc("/callback", completeAuth)
	http.HandleFunc("/favicon", favicon)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Got request for:", r.URL.String())
	})

	// See if saved auth is still good
	go func() {
		token := trySavedAuth(context.Background())

		if token.AccessToken == "" {
			go func() {
				err := http.ListenAndServe(":8080", nil)
				if err != nil {
					log.Fatal(err)
				}
			}()

			url := auth.AuthURL(state)
			err := exec.Command("open", url).Start()
			if err != nil {
				log.Printf("error running command: %+v", err)
			}
		}
	}()

	// this waits for the client to be provided in the channel
	// and then handles the main functionality (getting true/false and sending
	// through rch channel)
	go func() {
		// wait for auth to complete (either saved auth or user-interacted auth)
		client := <-ch
		// write the token first
		token, err := client.Token()
		if err != nil {
			log.Printf("Error using token: %+v", err)
		} else {
			oldToken, _ := client.Token()
			if token != oldToken {
				file, _ := json.MarshalIndent(token, "", " ")
				log.Println("Updating saved token")
				writeFile(authfile, file)
			}
		}
		state, _ := client.PlayerCurrentlyPlaying(context.Background())
		if state.Item == nil {
			// there must not be anything playing
			rch <- "false"
			return
		}
		currentItem := state.Item
		isSaved, err := client.UserHasTracks(context.Background(), currentItem.ID)
		if err != nil {
			log.Panicf("Error getting currently playing song: %+v", err)
		}
		rch <- fmt.Sprintf("%t", isSaved[0])
	}()

	// Either the saved auth or new auth will send the result through the channel
	result := <-rch
	fmt.Print(result)
}

func trySavedAuth(ctx context.Context) *oauth2.Token {
	tokenFile := readFile(authfile)
	token := &oauth2.Token{}
	_ = json.Unmarshal([]byte(tokenFile), token)
	// use the token to get an authenticated client
	client := spotify.New(auth.Client(ctx, token))
	user, err := client.CurrentUser(context.Background())
	if user == nil {
		log.Printf("Saved token didn't work: %+v", err)
		return token
	}
	state, _ := client.PlayerCurrentlyPlaying(context.Background())
	if state == nil {
		log.Printf("User is probably not playing anything")
		ch <- client
		return token
	}
	ch <- client
	return token
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
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
	client := spotify.New(auth.Client(r.Context(), tok))
	fmt.Fprint(w, loginPageHTML)
	ch <- client
}

func favicon(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Not found")
}

func readFile(filePath string) []byte {
	filePath = os.Getenv("HOME") + "/" + filePath
	// Read the content of the file
	content, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Error reading the file: %v", err)
			os.Exit(1)
		}
	}
	return content
}

func writeFile(filePath string, contents []byte) {
	filePath = os.Getenv("HOME") + "/" + filePath
	err := os.WriteFile(filePath, contents, 0644)
	if err != nil {
		log.Fatalf("Error writing file: %+v", err)
	}
}
