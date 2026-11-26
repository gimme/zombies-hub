// zombies-server hosts co-op games of Call of Duty Zombies. Players open its web page, group into rooms and start
// games together. For each room's game the server runs a dedicated server of the game under Wine: BO1Zombies.exe,
// Plutonium's server for a room on a Black Ops II map, or Steam's on BOIII for one on a Black Ops III map. Players
// connect only to the server, which relays their packets to the games.
//
//   - rooms.go: the rooms and their members, which the page shows, and each room's game.
//   - internal/bo1zombies: the Black Ops engine build. The server downloads it the first time it starts.
//   - game.go: runs a game, writes its output to a log file, and kills it when it stops answering.
//   - t6.go: a Black Ops II room's game, on Plutonium. The server downloads Plutonium and keeps it up to date.
//   - t7.go: a Black Ops III room's game, on BOIII, which the server downloads and keeps up to date too.
//   - relay.go: relays the players' packets to the UDP port of their room's game, which only the server reaches.
//     Browsers send their packets over WebTransport, and Windows players over UDP.
//   - web.go: serves the web client, the game files it downloads (files.go) and the rooms' API, over HTTP.
//   - launcher.go: the server's side of the launcher (launcher/), a program for a player's PC. The launcher starts the
//     player's own copy of Black Ops II and joins their room's game. It downloads the game files from the server when
//     the PC has none.
//
// Usage:
//
//	zombies-server [flags] [-- engine command line, e.g. +set g_debug 1]
//	zombies-server -t7-fetch [flags]                   (t7.go: fetches what Black Ops III's server downloads, and ends)
//	zombies-server game-files <folder> <dir>           (gamefiles.go: lists what the server takes of a game's folder)
//	zombies-server probe [-timeout 10s] <host:port>   (probe.go)
//	zombies-server console [command]                   (console.go)
//	zombies-server list-files <dir> [map...]           (files.go: lists what each map reads, for server/reads/)
//
// Each flag takes its default from an environment variable (ZOMBIES_...), for the container. Each line on stdin is a
// console command, and so is each line from the console's socket (console.go). A line goes to the console of every
// running game, or to one room's game when the line starts with the room's code. "rooms" lists the rooms, and "lag"
// sets the lag that the relay adds (lag.go). SIGINT or SIGTERM stops the games, which tell their clients, and then
// stops the server.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the time zones, so the log files' times follow TZ even without the image's zoneinfo

	"zombies/internal/bo1zombies"
	"zombies/internal/boiii"
	"zombies/internal/launcherapi"
	"zombies/internal/plutonium"
)

// titleDir returns the folder of a title's game files, the title's install or a copy of it. It is the folder in
// gameDir that is named after the title (t5 in t5.go, t6 in t6.go, t7 in t7.go). Steam's dedicated server of Black Ops
// III has a folder there as well (t7Server).
func titleDir(gameDir, title string) string { return filepath.Join(gameDir, title) }

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout) // so the log stays in order with the games' output
	// quic-go asks for 7 MB UDP buffers, for bulk transfers, and warns when it gets less (a container gets 416 kB).
	// The game traffic of a few players needs far less, so the warning is turned off.
	if _, set := os.LookupEnv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING"); !set {
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	}
	if len(os.Args) > 1 && os.Args[1] == "probe" {
		os.Exit(probe(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "list-files" {
		os.Exit(listFilesCommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "console" {
		os.Exit(consoleCommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "game-files" {
		os.Exit(gameFilesCommand(os.Args[2:]))
	}
	os.Exit(run())
}

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

// getEngine returns the pinned engine build, or the one in the archive file if file is set. The build is kept in the
// folder engine of dataDir and is downloaded the first time, from url or else from the releases of bo1-zombies.
func getEngine(ctx context.Context, dataDir, url, file string) (*bo1zombies.Engine, error) {
	if url == "" {
		url = bo1zombies.URL
	}
	src := bo1zombies.Source{Version: bo1zombies.Version, SHA256: bo1zombies.SHA256, URL: url, File: file,
		Client: &http.Client{Timeout: 10 * time.Minute}, Logf: log.Printf}
	return src.Get(ctx, filepath.Join(dataDir, "engine"))
}

// gamePorts returns the games' UDP ports on 127.0.0.1: one for each of the games that run at once, from first up. It
// leaves out the server's own port. It also leaves out the ports that Plutonium's servers open on every address, from
// plutoniumPort up (t6.go: the comment at the top). Those are one for each game, and one more, because they skip the
// server's own port when it is among them. A game whose port a Plutonium server took would listen on another port,
// where the server does not reach it.
func gamePorts(first, games, port int) []int {
	var ports []int
	for p := first; len(ports) < games && p <= 65535; p++ {
		if p != port && (p < plutoniumPort || p > plutoniumPort+games) {
			ports = append(ports, p)
		}
	}
	return ports
}

func run() int {
	port := flag.Int("port", envInt("ZOMBIES_PORT", 3075), "the port players reach: UDP for the games (WebTransport, and Windows players), TCP for the web client and game files (ZOMBIES_PORT)")
	publicPort := flag.Int("public-port", envInt("ZOMBIES_PUBLIC_PORT", 0), "the UDP port players reach WebTransport at, when a port forward or mapping makes it other than -port; 0: -port (ZOMBIES_PUBLIC_PORT)")
	mapName := flag.String("map", env("ZOMBIES_MAP", "zombie_theater"), "the map a new room starts with (ZOMBIES_MAP)")
	maxPlayers := flag.Int("max-players", envInt("ZOMBIES_MAX_PLAYERS", 4), "most players a room's host may set it to take, 1 to 8; a new room takes 4, or as near as this and -min-players allow. Past 4, the level's four characters and spawn points take more than one player each. A room on a Black Ops II map of Grief may take 8 whatever this is: four in each team (ZOMBIES_MAX_PLAYERS)")
	minPlayers := flag.Int("min-players", envInt("ZOMBIES_MIN_PLAYERS", 2), "players a room's game needs to start, and the fewest a room's host may set it to take; 1 lets a player start one alone, for tests (ZOMBIES_MIN_PLAYERS)")
	maxGames := flag.Int("max-games", envInt("ZOMBIES_MAX_GAMES", 4), "most games running at once (ZOMBIES_MAX_GAMES)")
	loadTimeout := flag.Duration("load-timeout", envDuration("ZOMBIES_LOAD_TIMEOUT", 5*time.Minute), "how long a room's game waits for its players to load before it is called off (ZOMBIES_LOAD_TIMEOUT)")
	gameArgs := flag.String("game-args", env("ZOMBIES_GAME_ARGS", ""), "more of the engine's command line, before the arguments after -- (ZOMBIES_GAME_ARGS)")
	gamePort := flag.Int("game-port", envInt("ZOMBIES_GAME_PORT", 3100), "the first game's UDP port, on 127.0.0.1, which only the server reaches; more games take the ports above it, and none takes one of the ports from 3074 that Plutonium's servers open (ZOMBIES_GAME_PORT)")
	gameDir := flag.String("game-dir", env("ZOMBIES_GAME_DIR", "/game"), "the game files, a folder for each title the server hosts, at least one: t5, a Black Ops install; t6, a Black Ops II install, which rooms play on Plutonium; t7, a Black Ops III install, which rooms play on BOIII, with Steam's Unranked Dedicated Server of it in t7-server; only read (ZOMBIES_GAME_DIR)")
	plutoniumDir := flag.String("plutonium-dir", env("ZOMBIES_PLUTONIUM_DIR", ""), "where the server keeps its copy of Plutonium, which it downloads for Black Ops II rooms; empty: plutonium in -data-dir (ZOMBIES_PLUTONIUM_DIR)")
	boiiiDir := flag.String("boiii-dir", env("ZOMBIES_BOIII_DIR", ""), "where the server keeps its copy of BOIII, which it downloads for Black Ops III rooms, and what Steam's server of that game fetched; empty: boiii in -data-dir (ZOMBIES_BOIII_DIR)")
	fetchT7 := flag.Bool("t7-fetch", false, "fetch what Steam's server of Black Ops III downloads from the game's publisher as it starts, unless the server has it, and end; the image's entrypoint does it before its firewall is up")
	engineURL := flag.String("engine-url", env("ZOMBIES_ENGINE_URL", ""), "where the server downloads the Black Ops engine from, with {version} replaced by the release's version; empty: the releases of bo1-zombies on GitHub (ZOMBIES_ENGINE_URL)")
	engineFile := flag.String("engine-file", env("ZOMBIES_ENGINE_FILE", ""), "an engine archive to run instead of the downloaded build, for work on the engine (ZOMBIES_ENGINE_FILE)")
	dataDir := flag.String("data-dir", env("ZOMBIES_DATA_DIR", "/config"), "where the server writes its game data, logs and database (zombies.db), and keeps the engine (ZOMBIES_DATA_DIR)")
	webPage := flag.String("web-page", env("ZOMBIES_WEB_PAGE", "/web"), "the folder of the web client's built page, with index.html (ZOMBIES_WEB_PAGE)")
	wine := flag.String("wine", env("ZOMBIES_WINE", "wine"), "the wine command (ZOMBIES_WINE)")
	consoleSocket := flag.String("console", env("ZOMBIES_CONSOLE", defaultConsole), `the console's Unix socket, for "zombies-server console"; "" for none (ZOMBIES_CONSOLE)`)
	dev := flag.Bool("dev", envInt("ZOMBIES_DEV", 0) != 0, "a dev run: the web client blinks what it leaves out of the picture, as the engine does, instead of drawing it black, and the server takes dev builds of the launcher (ZOMBIES_DEV=1)")
	cheats := flag.Bool("cheats", envInt("ZOMBIES_CHEATS", 0) != 0, "run games with cheats on (devmap), for tests (ZOMBIES_CHEATS=1)")
	downloadRate := flag.String("download-rate", env("ZOMBIES_DOWNLOAD_RATE", ""), "send browsers and launchers the game files, at most this many bits a second in total (40M, 500k, 1G), or unlimited; empty sends none (ZOMBIES_DOWNLOAD_RATE)")
	sendDir := flag.String("send-dir", env("ZOMBIES_SEND_DIR", ""), "game files that the server sends to launchers from another folder than its games read: a folder t7 there is a Black Ops III install, for a server whose t7 in -game-dir has only what a server reads of it; empty: it sends from -game-dir (ZOMBIES_SEND_DIR)")
	hangTimeout := flag.Duration("hang-timeout", envDuration("ZOMBIES_HANG_TIMEOUT", time.Minute), "how long a game may run no frame, or answer no one, before it ends; 0: never (ZOMBIES_HANG_TIMEOUT)")
	discordID := flag.String("discord-client-id", env("ZOMBIES_DISCORD_CLIENT_ID", ""), "have players sign in with Discord: your Discord application's client ID (needs -discord-client-secret) (ZOMBIES_DISCORD_CLIENT_ID)")
	// The default is not taken from the environment, because -h prints the defaults and would show the secret.
	discordSecret := flag.String("discord-client-secret", "", "the Discord application's client secret (ZOMBIES_DISCORD_CLIENT_SECRET)")
	discordServer := flag.String("discord-server", env("ZOMBIES_DISCORD_SERVER", ""), "only let members of this Discord server play (its ID) (ZOMBIES_DISCORD_SERVER)")
	discordRoles := flag.String("discord-roles", env("ZOMBIES_DISCORD_ROLES", ""), "only members with one of these roles (role IDs, separated by commas) (ZOMBIES_DISCORD_ROLES)")
	flag.Parse()
	if *discordSecret == "" {
		*discordSecret = os.Getenv("ZOMBIES_DISCORD_CLIENT_SECRET")
	}
	if *publicPort == 0 {
		*publicPort = *port
	}
	*maxPlayers = min(max(*maxPlayers, 1), maxRoomSize)
	// Each player of each game has a seat with a loopback address of its own (relay.go: acquire). There are 65,534 of
	// these addresses. A game of Grief may have more players than the server's rooms hold otherwise (lobby.sizeFor).
	*maxGames = min(*maxGames, 0xFFFE/maxRoomSize)
	downloads, rate, rateErr := parseRate(*downloadRate)
	if rateErr != nil {
		log.Printf("server: %v", rateErr)
		return 1
	}
	var discord *discordAuth
	if *discordID != "" || *discordSecret != "" || *discordServer != "" || *discordRoles != "" {
		if *discordID == "" || *discordSecret == "" {
			log.Printf("server: Discord sign-in needs both ZOMBIES_DISCORD_CLIENT_ID and ZOMBIES_DISCORD_CLIENT_SECRET")
			return 1
		}
		if *discordRoles != "" && *discordServer == "" {
			log.Printf("server: ZOMBIES_DISCORD_ROLES needs ZOMBIES_DISCORD_SERVER")
			return 1
		}
		// ZOMBIES_DISCORD_URL replaces Discord's address, for tests.
		discord = &discordAuth{clientID: *discordID, secret: *discordSecret, server: *discordServer,
			url: env("ZOMBIES_DISCORD_URL", "https://discord.com"), client: &http.Client{Timeout: discordTimeout}}
		for _, role := range strings.Split(*discordRoles, ",") {
			if role = strings.TrimSpace(role); role != "" {
				discord.roles = append(discord.roles, role)
			}
		}
	}
	con := &console{}
	output := io.MultiWriter(os.Stdout, con)
	log.SetOutput(output)

	// The server hosts each title that it has game files of. It needs at least one.
	t5Files, t6Files := titleDir(*gameDir, t5), titleDir(*gameDir, t6)
	st, err := os.Stat(filepath.Join(t5Files, "zone"))
	haveT5 := err == nil && st.IsDir()
	t6Maps := t6Installed(t6Files)
	t7Files, t7ServerFiles := titleDir(*gameDir, t7), titleDir(*gameDir, t7Server)
	t7Maps := t7Installed(t7Files, t7ServerFiles)
	if *fetchT7 && len(t7Maps) == 0 {
		return 0 // no Black Ops III, so nothing to fetch for it
	}
	if !haveT5 && len(t6Maps) == 0 && len(t7Maps) == 0 {
		log.Printf("server: no game files in %s: copy a Black Ops install to %s, a Black Ops II install to %s, or a Black Ops III install to %s and Steam's dedicated server of it to %s",
			*gameDir, t5Files, t6Files, t7Files, t7ServerFiles)
		return 1
	}
	if st, err := os.Stat(filepath.Join(t7Files, "zone")); len(t7Maps) == 0 && err == nil && st.IsDir() {
		log.Printf("server: no Black Ops III: %s has the game, and %s is to have Steam's dedicated server of it, its folder UnrankedServer (the tool Call of Duty: Black Ops III - Unranked Dedicated Server)",
			t7Files, t7ServerFiles)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	// Black Ops runs on the engine. A server without Black Ops does not download it.
	eng := &bo1zombies.Engine{}
	if haveT5 {
		if eng, err = getEngine(ctx, *dataDir, *engineURL, *engineFile); err != nil {
			log.Printf("server: %v", err)
			return 1
		}
	}

	gameUID, err := gamesUser()
	if err != nil {
		log.Printf("server: %v", err)
		return 1
	}

	// T7, when the game files have it. A T7 room's game is Steam's dedicated server on BOIII, which the server
	// downloads when the first T7 game starts. ZOMBIES_BOIII_URL replaces the address of the manifest of BOIII's
	// updater, for tests. One port more than the rooms' games have is for the run that fetches what Steam's server
	// downloads (t7Fetch).
	ports := gamePorts(*gamePort, *maxGames+1, *port)
	var t7Boiii *t7Copy
	var newT7 func(code, mapName, home string, port, size int, password string) *game
	if len(t7Maps) > 0 {
		if *boiiiDir == "" {
			*boiiiDir = filepath.Join(*dataDir, "boiii")
		}
		t7Boiii = &t7Copy{Copy: &boiii.Copy{Dir: *boiiiDir, URL: env("ZOMBIES_BOIII_URL", boiii.Manifest),
			Client: &http.Client{Timeout: 10 * time.Minute}, State: "zombies-server-files.json", Logf: log.Printf},
			exe: filepath.Join(t7ServerFiles, filepath.FromSlash(t7Exe))}
		newT7 = func(code, mapName, home string, port, size int, password string) *game {
			g := &game{
				name: code, wine: *wine, uid: gameUID, dir: t7Files, logDir: filepath.Join(*dataDir, "logs"),
				home: home, ip: "127.0.0.1", port: port, output: prefixed{"[" + code + "] ", output},
				engine:       newT7Game(t7Boiii, t7ServerFiles, mapName, size, password),
				startTimeout: max(5*time.Minute, *hangTimeout),
			}
			if *hangTimeout > 0 {
				g.quietTimeout = *hangTimeout + 10*time.Second
			}
			return g
		}
	}
	// fetchT7Files fetches what Steam's server downloads as it starts, unless the server's copy of BOIII has it for
	// this build of Steam's server (t7Copy.lpc). The
	// run's home is in the folder of that copy, and not among the rooms' games' homes: Wine runs a game from wherever
	// the copy is, which on Windows is a volume when the homes are not (docker/server.sh).
	fetchT7Files := func() error {
		if own := t7Boiii.own(); len(own) > 0 {
			log.Printf("server: Black Ops III's server has its files from the game's publisher with the game files (%s)",
				filepath.Dir(own[0]))
			return nil
		}
		if len(t7Boiii.fetched()) > 0 {
			return nil
		}
		log.Printf("server: Black Ops III's server fetches its files from the game's publisher, once (into %s)",
			t7Boiii.lpc())
		g := newT7("fetch", "", filepath.Join(*boiiiDir, "fetch"), ports[len(ports)-1], 1, randomHex(8))
		g.kind = "fetch"
		return t7Fetch(ctx, g)
	}
	if *fetchT7 {
		if err := fetchT7Files(); err != nil {
			log.Printf("server: %v", err)
			return 1
		}
		return 0
	}

	certs := &certificates{}
	_, hash, err := certs.current()
	if err != nil {
		log.Printf("server: no WebTransport certificate: %v", err)
		return 1
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{Port: *port})
	if err != nil {
		log.Printf("server: %v", err)
		return 1
	}
	tcp, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Printf("server: %v", err)
		return 1
	}

	failed := make(chan error, 2)

	l := newLobby(ctx)
	l.roomSize, l.defaultMap, l.loadTimeout, l.discord = *maxPlayers, *mapName, *loadTimeout, discord
	l.minPlayers = max(1, min(*minPlayers, l.roomSize))
	// The players are in the database. A server from before the database kept them in players.json (import.go).
	players, err := openStore(filepath.Join(*dataDir, "zombies.db"))
	if err == nil {
		defer players.close()
		err = players.importPlayers(filepath.Join(*dataDir, "players.json"))
	}
	if err == nil {
		err = l.loadPlayers(players)
	}
	if err != nil {
		log.Printf("server: can't read the players: %v", err)
		return 1
	}
	l.gamePorts = ports[:len(ports)-1]
	if haveT5 {
		l.t5().newGame = func(code, mapName string, port, players int, mods []string) *game {
			// sv_timeout: the game keeps the character of a dropped player for keepCharacter
			// (bo1-zombies/src/server_mp/sv_lobby_mp.cpp). sv_connectTimeout: a player who is loading the map sends the
			// game nothing until the map is loaded, so the game waits for them as long as the server does (loadTimeout).
			// The game's own timeout of 80 s dropped a browser that was loading Call of the Dead behind its movie.
			args := []string{"+set", "sv_maxclients", strconv.Itoa(l.roomSize), "+set", "bo1_lobby", "1",
				"+set", "bo1_players", strconv.Itoa(players), "+set", "sv_timeout", strconv.Itoa(int(keepCharacter.Seconds())),
				"+set", "sv_connectTimeout", strconv.Itoa(min(int(l.loadTimeout.Seconds()), 1800))}
			args = append(args, modArgs(mods)...)
			args = append(args, strings.Fields(*gameArgs)...)
			args = append(args, flag.Args()...)
			args = append(args, map[bool]string{false: "+map", true: "+devmap"}[*cheats], mapName)
			// Each port has a home folder of its own, so that games running at the same time never write the same file.
			// Each game empties the folder when it starts (runOnce).
			home := filepath.Join(*dataDir, "game", strconv.Itoa(slices.Index(l.gamePorts, port)+1))
			g := &game{
				name: code, wine: *wine, uid: gameUID, dir: t5Files, home: home, logDir: filepath.Join(*dataDir, "logs"),
				ip: "127.0.0.1", port: port, output: prefixed{"[" + code + "] ", output},
				engine:       &t5Engine{exe: eng.Exe(), args: args, hangTimeout: *hangTimeout},
				startTimeout: max(5*time.Minute, *hangTimeout),
			}
			if *hangTimeout > 0 {
				g.quietTimeout = *hangTimeout + 10*time.Second // so the game's own report of a hang comes first
			}
			return g
		}
	}
	// T6, when the game files have it. A T6 room's game is Plutonium's server, which the server downloads when the
	// first T6 game starts. ZOMBIES_PLUTONIUM_URL replaces the address of the manifest of Plutonium's updater, for tests.
	if len(t6Maps) > 0 {
		if *plutoniumDir == "" {
			*plutoniumDir = filepath.Join(*dataDir, "plutonium")
		}
		pluto := &t6Copy{Copy: &plutonium.Copy{Dir: *plutoniumDir, URL: env("ZOMBIES_PLUTONIUM_URL", plutonium.Manifest),
			Needs: plutonium.T6, Client: &http.Client{Timeout: 10 * time.Minute}, State: "zombies-server-files.json",
			Logf: log.Printf}}
		l.t6().pluto = pluto
		l.t6().newGame = func(code, mapName string, port, players int, password string, mods []string) *game {
			g := &game{
				name: code, wine: *wine, uid: gameUID, dir: t6Files, logDir: filepath.Join(*dataDir, "logs"),
				home: filepath.Join(*dataDir, "game", strconv.Itoa(slices.Index(l.gamePorts, port)+1)),
				ip:   "127.0.0.1", port: port, output: prefixed{"[" + code + "] ", output},
				engine:       newT6Game(pluto, mapName, players, l.sizeFor(mapName), l.loadTimeout, password, mods),
				startTimeout: max(5*time.Minute, *hangTimeout),
			}
			if *hangTimeout > 0 {
				g.quietTimeout = *hangTimeout + 10*time.Second
			}
			return g
		}
	}
	if len(t7Maps) > 0 {
		l.t7().copy = t7Boiii
		l.t7().newGame = func(code, mapName string, port, players int, password string) *game {
			home := filepath.Join(*dataDir, "game", strconv.Itoa(slices.Index(l.gamePorts, port)+1))
			return newT7(code, mapName, home, port, l.sizeFor(mapName), password)
		}
		// Steam's server has what it downloads from the entrypoint's run, before the image's firewall was up
		// (-t7-fetch). A server that runs without the entrypoint fetches it now, and its rooms play Black Ops III
		// once it has.
		go func() {
			if err := fetchT7Files(); err != nil {
				log.Printf("server: no Black Ops III: %v. It fetches them where the games reach the internet: as the image's container starts, before its firewall is up", err)
			}
			l.mu.Lock()
			l.change()
			l.mu.Unlock()
		}()
	}
	pages := &web{certs: certs, publicPort: *publicPort, pageDir: *webPage, buildDir: eng.Web,
		pipelinesDir: eng.Pipelines, gameDir: t5Files,
		dev: *dev, lobby: l, downloads: downloads, pace: &pacer{rate: rate},
		hashes: fileHashes{file: filepath.Join(*dataDir, "hashes.tsv")}}
	if len(t6Maps) > 0 {
		pages.t6Dir = t6Files
	}
	// A player's game needs far more of Black Ops III than its server reads. The server sends the game from the folder
	// that has it all: the one its games read, or the one in -send-dir.
	if len(t7Maps) > 0 && downloads {
		from := t7Files
		if dir := titleDir(*sendDir, t7); *sendDir != "" && t7Sendable(dir) {
			from = dir
		}
		if t7Sendable(from) {
			pages.t7Send = from
		} else {
			log.Printf("server: the server sends no game files of Black Ops III: %s has what a server reads of the game, and not what a player's game needs (the whole install; ZOMBIES_SEND_DIR names another folder that has one, as t7)", t7Files)
		}
	}
	l.sends = pages.sentPacks
	t5Maps := installedMaps(pages.paths)
	l.maps = func() []string { return slices.Concat(t5Maps(), t6Maps, t7Maps) }
	l.mapDesc = pages.mapDescription
	reads, err := committedReads()
	if err != nil {
		log.Printf("server: %v", err)
		return 1
	}
	l.files = newFileLists(reads, pages.files)
	l.files.sums.file = filepath.Join(*dataDir, "pieces.tsv")

	rl := newRelay(l, certs)
	l.relay = rl
	go func() {
		if err := rl.serve(udp); err != nil {
			failed <- err
		}
	}()
	httpServer := &http.Server{Handler: pages, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpServer.Serve(tcp); !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()
	go l.sweep()
	go l.keepPlayers()
	// Make each map's list of what it reads now, not when the first browser asks for it (files.go). This also hashes
	// the game files, which takes a while the first time (hashes.go).
	go func() {
		for _, mapName := range t5Maps() {
			if l.files.lookup(mapName) == nil {
				log.Printf("files: no list of what %s reads (server/reads/: docker/list-files.sh makes one): a browser can't play it", mapName)
			}
		}
	}()
	// List the game files that the server sends to launchers now too (launcher.go). Hashing them takes a while the
	// first time: a minute for Black Ops II, and several for Black Ops III. The page then has their sizes.
	go func() {
		for _, of := range []string{t6, t7} {
			s := pages.sending(of)
			if s == nil {
				continue
			}
			list := pages.listFiles(s)
			_, packs := pages.sentPacks(of)
			each := ""
			if len(packs) > 1 {
				each = fmt.Sprintf(": %.1f GB that every map needs, and the rest a map at a time", float64(packs[""])/1e9)
			}
			log.Printf("files: the launcher downloads %s's game files to a PC without them, from %s: %d files, %.1f GB%s",
				s.label, s.dir, len(list.Files.Files), float64(list.Bytes)/1e9, each)
			l.mu.Lock()
			l.change()
			l.mu.Unlock()
		}
	}()
	// Make the players' pictures now too (pictures.go), after the relay and the HTTP server have started. The pictures
	// are read from the archives in the game files, and that waits for the hashes. Until the pictures are made the
	// server has none. havePictures then gives one to each player who needs one.
	go func() {
		have := pages.playerPictures()
		var lack []string
		for _, p := range pictureSet {
			if !slices.Contains(have, p.id) {
				lack = append(lack, p.id)
			}
		}
		if haveT5 && len(lack) > 0 {
			log.Printf("server: the game files lack %d of the players' %d pictures: %s", len(lack), len(pictureSet),
				strings.Join(lack, ", "))
		}
		l.havePictures(have)
	}()
	log.Printf("server: players join on port %d (UDP: the games, from browsers over WebTransport and from Windows; TCP: the web client and game files, http://<host>:%d/); certificate sha-256 %s",
		*port, *port, hash)
	if *publicPort != *port {
		log.Printf("server: web clients join at UDP port %d", *publicPort)
	}
	log.Printf("server: rooms of up to %d players, %d games at once; maps: %s", l.roomSize, len(l.gamePorts),
		strings.Join(l.maps(), ", "))
	if haveT5 {
		log.Printf("server: Black Ops runs on engine %s (build %s)", eng.Version, eng.Build)
	}
	if len(t6Maps) > 0 {
		log.Printf("server: rooms play Black Ops II on Plutonium (its copy: %s)", *plutoniumDir)
		takes := fmt.Sprintf("version %d and later", launcherapi.MinVersion)
		if *dev {
			takes += ", and dev builds"
		}
		log.Printf("server: its players join from their PCs with the launcher, from %s (%s)", launcherapi.Releases, takes)
	}
	if len(t7Maps) > 0 {
		log.Printf("server: rooms play Black Ops III on BOIII (its copy: %s); their players join from their PCs with the launcher, from version %d",
			*boiiiDir, launcherapi.MinVersionOf(t7))
	}
	if gameUID != 0 {
		log.Printf("server: the games run as user %d, the server as %d", gameUID, os.Getuid())
	} else {
		log.Printf("server: the games run as the server's user (%d), so a game taken over could read the database: the image's entrypoint, run as root, has them run as a user of their own", os.Getuid())
	}
	switch {
	case !downloads:
		log.Printf("server: the server sends no game files (-download-rate); players import them from their own copy of the game")
	case rate == 0:
		log.Printf("server: browsers download the game files of the maps they play, as fast as they can")
	default:
		log.Printf("server: browsers download the game files of the maps they play, at up to %s a second in all", *downloadRate)
	}
	switch {
	case discord == nil:
		log.Printf("server: anyone can play, under a name they choose")
	case discord.server == "":
		log.Printf("server: players sign in with Discord: any account can play")
	case len(discord.roles) == 0:
		log.Printf("server: players sign in with Discord: members of server %s can play", discord.server)
	default:
		log.Printf("server: players sign in with Discord: members of server %s with role %s can play",
			discord.server, strings.Join(discord.roles, " or "))
	}

	go func() {
		// When stdin ends (there is none, it is /dev/null, or its pipe is closed), the server carries on without it.
		in := bufio.NewScanner(os.Stdin)
		for in.Scan() {
			if line := strings.TrimSpace(in.Text()); line != "" {
				l.command(line)
			}
		}
	}()
	if *consoleSocket != "" {
		os.Remove(*consoleSocket) // left by a server that was killed
		if ln, err := net.Listen("unix", *consoleSocket); err != nil {
			log.Printf("server: no console socket: %v", err)
		} else {
			defer ln.Close()
			go con.serve(ln, l.command)
		}
	}

	code := 0
	select {
	case <-ctx.Done():
	case err := <-failed:
		log.Printf("server: %v", err)
		code = 1
		cancel()
	}
	l.games.Wait() // each game stops when ctx is done
	if err := l.writePlayers(); err != nil {
		log.Printf("server: %v", err)
	}
	rl.close()
	shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	httpServer.Shutdown(shutdown)
	log.Printf("server: stopped")
	return code
}
