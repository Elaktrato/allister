package game

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/allister/allister/internal/plugin"
	"github.com/allister/allister/internal/plugin/plugintest"
)

// TestNewProvidesDeclaredSourcesImportersAndExporter: the plugin wires what its
// manifest declares, leaves unknown ids nil, keeps the account importer's
// optional capability, and refuses a second registration.
func TestNewProvidesDeclaredSourcesImportersAndExporter(t *testing.T) {
	p := New(Options{})
	for _, id := range []string{SourceIGDB, SourceSteam} {
		if p.Source(id) == nil {
			t.Fatalf("source %q not wired", id)
		}
	}
	for _, id := range []string{ImporterYamtrack, ImporterSteam} {
		if p.Importer(id) == nil {
			t.Fatalf("importer %q not wired", id)
		}
	}
	if _, ok := p.Importer(ImporterSteam).(plugin.AccountImporter); !ok {
		t.Fatal("Steam importer does not implement AccountImporter")
	}
	if p.Exporter(ExporterJSON) == nil {
		t.Fatal("JSON exporter not wired")
	}
	if p.Exporter("nope") != nil || p.Source("nope") != nil || p.Importer("nope") != nil {
		t.Fatal("unknown ids must be nil")
	}
	if err := plugin.Validate(p.Manifest()); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	// Nil key funcs mean no credentials: the keyed sources report
	// ErrNotConfigured instead of calling a server.
	ctx := context.Background()
	if _, err := p.Source(SourceIGDB).Details(ctx, KindGame, "1"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("keyless IGDB details err = %v, want ErrNotConfigured", err)
	}
	if _, err := p.Source(SourceSteam).Details(ctx, KindGame, "1"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("keyless Steam details err = %v, want ErrNotConfigured", err)
	}

	reg := plugin.NewRegistry(plugintest.NewMemEnabled())
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(New(Options{})); err == nil {
		t.Fatal("second games registration should be refused")
	}
}

// TestNewResolvesCredentialsPerRequest: the key funcs are consulted on every
// request, so a key saved after startup takes effect without a rebuild. Before
// they answer, both keyed sources are not configured; after, the fake servers'
// data comes back.
func TestNewResolvesCredentialsPerRequest(t *testing.T) {
	igdbRec := &igdbTestRecorder{}
	tokenSrv, dataSrv := newIGDBTestServers(t, igdbRec, igdbOKToken("token", 3600), func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/games":
			io.WriteString(w, `[{"id":42,"name":"Celeste","slug":"celeste","first_release_date":1515974400}]`)
		case "/game_time_to_beats":
			io.WriteString(w, `[{"hastily":3600}]`)
		default:
			http.NotFound(w, r)
		}
	})
	steamRec := &steamTestRecorder{}
	_, storeSrv := newSteamTestServers(t, steamRec, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/appdetails" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"7":{"success":true,"data":{"type":"game","name":"Portal 2","steam_appid":7}}}`)
	})

	var clientID, clientSecret, steamKey string
	p := New(Options{
		IGDBURL: dataSrv.URL, IGDBTokenURL: tokenSrv.URL,
		SteamStoreURL: storeSrv.URL,
		IGDBClientID:  func(context.Context) (string, error) { return clientID, nil },
		IGDBClientSecret: func(context.Context) (string, error) {
			return clientSecret, nil
		},
		SteamKey: func(context.Context) (string, error) { return steamKey, nil },
	})

	ctx := context.Background()
	if _, err := p.Source(SourceIGDB).Details(ctx, KindGame, "42"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("IGDB before keys: err = %v, want ErrNotConfigured", err)
	}
	if _, err := p.Source(SourceSteam).Details(ctx, KindGame, "7"); !errors.Is(err, plugin.ErrNotConfigured) {
		t.Fatalf("Steam before key: err = %v, want ErrNotConfigured", err)
	}

	clientID, clientSecret, steamKey = "client-id", "client-secret", "steam-key"

	d, err := p.Source(SourceIGDB).Details(ctx, KindGame, "42")
	if err != nil {
		t.Fatalf("IGDB details: %v", err)
	}
	if d.Kind != KindGame || len(d.Titles) == 0 || d.Titles[0].Text != "Celeste" {
		t.Fatalf("IGDB details = %+v", d)
	}
	if n := d.Fields["time_main"].Num; n == nil || *n != 60 {
		t.Fatalf("IGDB time_main = %v, want 60 minutes", d.Fields["time_main"].Num)
	}
	if d.ExternalIDs["igdb"] != "42" {
		t.Fatalf("IGDB external ids = %v", d.ExternalIDs)
	}

	sd, err := p.Source(SourceSteam).Details(ctx, KindGame, "7")
	if err != nil {
		t.Fatalf("Steam details: %v", err)
	}
	if sd.Kind != KindGame || len(sd.Titles) == 0 || sd.Titles[0].Text != "Portal 2" {
		t.Fatalf("Steam details = %+v", sd)
	}

	// The resolved credentials reached the wire, and the Steam key stayed off
	// the keyless store request.
	if _, q, _ := igdbRec.tokenRequest(); q.Get("client_id") != "client-id" || q.Get("client_secret") != "client-secret" {
		t.Fatalf("token request credentials = %v", q)
	}
	if reqs := steamRec.all(); len(reqs) != 1 || reqs[0].query.Get("key") != "" {
		t.Fatalf("steam store requests = %+v", reqs)
	}
}
