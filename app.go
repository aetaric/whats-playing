package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/aetaric/go-plex-client"
	"github.com/aetaric/whats-playing/storage"
	"github.com/hugolgst/rich-go/client"
	"github.com/koffeinsource/go-imgur"
)

// App struct
type App struct {
	ctx           context.Context
	plex          plex.Plex
	pin           string
	status        string
	authToken     string
	server        string
	chosen_server plex.PMSDevices
	username      string
	userid        int
	authorized    bool
	imgurClient   *imgur.Client
	servers       []plex.PMSDevices
	storage       storage.Storage
	sessionActive bool
	session       string
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.status = "Please link your plex account to get started."
	err := client.Login("413407336082833418")
	if err != nil {
		panic(err)
	}
	imgurClient, err := imgur.NewClient(new(http.Client), "0dedf5b51d09876", "")
	if err != nil {
		fmt.Printf("failed during imgur client creation. %+v\n", err)
		return
	}
	a.imgurClient = imgurClient

	a.storage = storage.Storage{}
	a.storage.Open()

	token := a.storage.Get([]byte("plex-token"), []byte("token"))
	if token != nil {
		a.status = "Token loaded from storage"
		a.authToken = string(token)
		a.getServersFromPlex()
	}
}

func (a *App) LinkPlex() {
	// get Plex headers
	p, err := plex.New("", "abc123")
	if err != nil {
		panic(err)
	}

	// Get PIN
	info, err := plex.RequestPIN(p.Headers)

	if err != nil {
		panic("request plex pin failed: " + err.Error())
	}

	expireAtParsed, err := time.Parse(time.RFC3339, info.ExpiresAt)

	if err != nil {
		panic("could not get expiration for plex pin")
	}

	expires := time.Until(expireAtParsed).String()

	fmt.Printf("your pin %s and expires in %s\n", info.Code, expires)
	a.pin = info.Code
	a.status = fmt.Sprintf("Please Navigate to https://plex.tv/link and provide the pin: %s", info.Code)

	var authToken string
	for {
		pinInformation, _ := plex.CheckPIN(info.ID, p.ClientIdentifier)

		if pinInformation.AuthToken != "" {
			authToken = pinInformation.AuthToken
			break
		}

		time.Sleep(1 * time.Second)
	}

	a.status = "You have been successfully authorized!"
	fmt.Println("Authorized.")
	a.authToken = authToken
	a.storage.Set([]byte("plex-token"), []byte("token"), []byte(authToken))
	a.getServersFromPlex()
}

func (a *App) getServersFromPlex() {
	// Get list of servers from plex
	server_plex, err := plex.New("", a.authToken)

	if err != nil {
		panic("Auth token went bad")
	}

	user, err := server_plex.MyAccount()

	if err != nil {
		panic("failed getting user data")
	}

	a.username = user.Username
	a.userid = user.ID

	servers, err := server_plex.GetServers()

	if err != nil {
		panic("failed getting plex servers")
	}

	a.servers = servers
	a.authorized = true
}

// Display Status
func (a *App) GetStatus() string {
	return a.status
}

func (a *App) IsAuthorized() bool {
	return a.authorized
}

// Display Servers
func (a *App) GetServers() []string {
	var servers []string
	servers = append(servers, "")
	for _, server := range a.servers {
		servers = append(servers, server.Name)
	}
	return servers
}

// Pick Server
func (a *App) SetServer(server string) {
	for _, s := range a.servers {
		if s.Name == server {
			a.chosen_server = s
			a.server = server
		}
	}
	a.Listener()
}

func (a *App) connectToPlexServers() {
	for _, addr := range a.chosen_server.Connection {
		plex, err := plex.New(addr.URI, a.authToken)
		if err == nil {
			_, err := plex.GetSessions()
			if err == nil {
				a.plex = *plex
				fmt.Printf("Set plex connection string to: %s\n", addr.URI)
				a.status = fmt.Sprintf("Listening for events from %s for %s", a.server, a.username)
				return
			} else {
				fmt.Printf("Failed to connect to server at address: %s\n", addr.URI)
			}
		} else {
			fmt.Println("unable to connect to plex server")
		}
	}
	a.status = "Failed to connect to specified server. The plex API only supports getting session data for servers you own."
}

// Get Imgur Thumbnail URL
func (a *App) getImgurURL(meta plex.Metadata) []byte {
	thumbnail := "logo"
	if meta.Type == "episode" {
		thumbnail = meta.GrandparentThumb
	} else {
		if meta.Thumb != "" {
			thumbnail = meta.Thumb
		} else if meta.ParentThumb != "" {
			thumbnail = meta.ParentThumb
		} else {
			thumbnail = meta.GrandparentThumb
		}
	}

	imgurURL := a.storage.Get([]byte("imgur-urls"), []byte(thumbnail))

	if (imgurURL == nil) || (fmt.Sprintf("%v", imgurURL) == "logo") {
		thumbURL := fmt.Sprintf("%s%s?X-Plex-Token=%s", a.plex.URL, thumbnail, a.authToken)

		resp, err := http.Get(thumbURL)
		if err != nil {
			fmt.Println("Error fetching image data from plex")
			return []byte("logo")
		}

		defer resp.Body.Close()

		imageData, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println("Error reading image data from plex")
			return []byte("logo")
		}

		imgurData, _, err := a.imgurClient.UploadImage(imageData, "", "URL", thumbnail, "")
		if err != nil {
			fmt.Println(err)
			a.storage.Set([]byte("imgur-urls"), []byte("logo"), []byte("logo"))
			return []byte("logo")
		}

		a.storage.Set([]byte("imgur-urls"), []byte(thumbnail), []byte(imgurData.Link))
		return []byte(imgurData.Link)
	}

	return imgurURL
}

func getMediaTitle(meta plex.Metadata, sessionType string) string {
	switch sessionType {
	case "track":
		return fmt.Sprintf("%s - %s", meta.GrandparentTitle, meta.Title)
	case "movie":
		return fmt.Sprintf("%s (%v)", meta.Title, meta.Year)
	case "episode":
		seasonNum := fmt.Sprintf("%02d", meta.ParentIndex)
		episodeNum := fmt.Sprintf("%02d", meta.Index)
		return fmt.Sprintf("%s S%sE%s - %s", meta.GrandparentTitle, seasonNum, episodeNum, meta.Title)
	default:
		return "Unknown Media Title"
	}
}

func getMediaLargeText(sessionType string) string {
	switch sessionType {
	case "track":
		return "Listening to Music"
	case "movie":
		return "Watching a Movie"
	case "episode":
		return "Watching a TV Show"
	default:
		return "Unknown Media Type"
	}
}

func (a *App) isUserActive(sessions []plex.Metadata, username string) bool {
	for _, session := range sessions {
		if session.User.Title == username {
			a.session = session.Session.ID
			return true
		}
	}
	return false
}

func (a *App) handlePlayingNotification(n plex.NotificationContainer) {
	mediaID := n.PlaySessionStateNotification[0].RatingKey
	sessionID := n.PlaySessionStateNotification[0].SessionKey
	viewOffset := n.PlaySessionStateNotification[0].ViewOffset
	state := n.PlaySessionStateNotification[0].State

	sessions, err := a.plex.GetSessions()
	if err != nil {
		fmt.Printf("failed to fetch sessions on plex server: %v\n", err)
		return
	}

	a.sessionActive = a.isUserActive(sessions.MediaContainer.Metadata, a.username)

	if a.sessionActive {
		for _, session := range sessions.MediaContainer.Metadata {
			a.handleSession(session, mediaID, sessionID, state, viewOffset)
		}
	}
}

func (a *App) handleSession(session plex.Metadata, mediaID string, sessionID string, state string, viewOffset int64) {
	var act client.Activity

	if a.username != session.User.Title {
		return
	}

	if sessionID != session.SessionKey {
		return
	}

	metadata, err := a.plex.GetMetadata(mediaID)

	if err != nil {
		fmt.Printf("failed to get metadata for key %s: %v\n", mediaID, err)
		return
	}

	meta := metadata.MediaContainer.Metadata[0]

	largeText := getMediaLargeText(session.Type)
	title := getMediaTitle(meta, session.Type)
	imgurURL := a.getImgurURL(meta)

	act.LargeText = largeText
	act.Details = title
	act.LargeImage = string(imgurURL)
	act.SmallText = cases.Title(language.AmericanEnglish).String(state)
	act.SmallImage = state

	if state != "paused" {
		t := time.Now().Add(-time.Duration(viewOffset * 1000 * 1000))
		timestamp := client.Timestamps{Start: &t}
		act.Timestamps = &timestamp
	}

	client.SetActivity(act)
}

func (a *App) CheckActiveSessions() {
	for {
		sessions, err := a.plex.GetSessions()

		if err != nil {
			return
		}

		var isActive bool
		for _, session := range sessions.MediaContainer.Metadata {
			if a.session == session.Session.ID {
				isActive = true
			}
		}

		if !isActive {
			var act client.Activity
			act.LargeText = "Idle"
			act.Details = "Idle"
			act.LargeImage = "logo"
			act.SmallText = ""
			act.SmallImage = ""
			client.SetActivity(act)
		}

		time.Sleep(1 * time.Second)
	}
}

func (a *App) Listener() {
	a.connectToPlexServers()
	ctrlC := make(chan os.Signal, 1)
	onError := func(err error) {
		fmt.Println(err)
	}
	events := plex.NewNotificationEvents()
	events.OnPlaying(func(n plex.NotificationContainer) {
		a.handlePlayingNotification(n)
	})
	go a.CheckActiveSessions()
	a.plex.SubscribeToNotifications(events, ctrlC, onError)
}
