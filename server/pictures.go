package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
)

// The players' pictures, which the page shows next to their names. Every player has one of a set of pictures made
// from the game files (pictureSet). The lobby gives a new player a random one (givePicture), and the player can pick
// another on the page (setPicture). When players sign in with Discord (discord.go), a player's avatar on Discord is
// their picture while they have an avatar, until they pick a picture of the set (rooms.go: avatar).
//
//	GET  /pictures/<id>.png  the picture of the set with that id, as a PNG of 128x128. It is made from
//	                         images/<image>.iwi in the archives (mapart.go), according to its kind (below). The
//	                         answer is a 404 for an id that is not in the set, or when the game files lack the image.
//	POST /lobby/picture      {"picture"}: the player picks that picture of the set. With "", the player's avatar on
//	                         Discord is their picture again (rooms.go).
//
// A picture's kind is what its texture is. The kind decides how the picture is framed (framed):
//
//	map    a map's picture in the game's map select, a 4:3 picture that the game stores square. The picture is the
//	       middle square of it, without the empty frame that the texture has around it.
//	crop   a part of a picture, which fills the square whatever its shape. It is a square of a calling card's strip
//	       (card), or a part of a picture that the game stores squeezed (squeezed): a zombie in a map's picture in the
//	       map select (3 texels wide for every 4 high), or a face on a loading screen (9 for every 16).
//	icon   a picture with alpha, like the HUD's icons. Only the part of it that shows is used (a texture may have
//	       room around its picture). It is as large as fits the circle that the page shows (fitted), in its own
//	       shape, in the middle of a transparent square.
//	shield an icon in the shape of a shield, like those of the perks and the power-ups: square at its top, a point
//	       at its foot. With its box in the middle of the square, it looks too high in the page's circle. Its two top
//	       corners are past the circle's edge and there is room on either side of its point, because most of it is
//	       above the middle of its box. It is centred on its weight instead (weightOf), the point where it would
//	       balance. That puts it 6 of the square's 128 pixels lower, with its corners inside the circle. (Centred in
//	       the smallest circle around it, with its corners and its point equally far from the edge, it would be 12
//	       pixels lower, which looks too low.)
//	chalk  what the game draws additively, chalk on a wall (a weapon's outline, the rounds' tally marks). A texel's
//	       brightness times its alpha is the amount of chalk there. It is drawn in the page's cream. The part of it
//	       that shows is framed like an icon and then made three times as strong (pictureChalkGain), so that the thin
//	       lines of an outline show.
//
// A crop is made from the part of its texture that its line in pictureSet gives, in texels. A picture of another kind
// is made from all of its texture, or from the part that its line gives (of) when a texture has two drawings.
//
// The page lists the pictures that the lobby has (view.Pictures), each under its group's heading. They are the
// pictures that the server could make from the game files (playerPictures). The server makes them when it starts and
// then gives them to the lobby (lobby.havePictures).

// picture is a picture of the set. It has the heading the page lists it under (group), the id that the server and the
// page know it by, its name (the page's tooltip), the texture it is made from (images/<image>.iwi), its kind (above),
// and the part of the texture it is made from, in texels. An empty part means the whole texture.
type picture struct {
	group, id, name, image, kind string
	part                         image.Rectangle
}

// pictureSet is the set of pictures, in the order the page lists it: group after group, one picture on each line.
// The server stores a player's picture as its id (store.go), so when an id changes, the players who had that
// picture no longer have it. Every picture is from Zombies: something the game shows, draws on its walls, or shows
// while a map loads.
var pictureSet = slices.Concat(
	// the maps' pictures in the game's map select
	group("Maps", mapPictures()...),
	group("Characters",
		// the four characters of the story: their portraits on Shi No Numa's loading screen, and their faces as
		// painted on the Matryoshka dolls
		onScreen("dempsey", "Dempsey", "sumpf", 843, 727, 210),
		onScreen("nikolai", "Nikolai", "sumpf", 160, 782, 220),
		onScreen("takeo", "Takeo", "sumpf", 835, 360, 220),
		onScreen("richtofen", "Richtofen", "sumpf", 188, 408, 220),
		crop("dempseydoll", "Dempsey Doll", "t5_weapon_nesting_bomb_dempsey_c", image.Rect(171, 130, 341, 300)),
		crop("nikolaidoll", "Nikolai Doll", "t5_weapon_nesting_bomb_nikolai_c", image.Rect(171, 130, 341, 300)),
		crop("takeodoll", "Takeo Doll", "t5_weapon_nesting_bomb_takeo_c", image.Rect(171, 130, 341, 300)),
		crop("richtofendoll", "Richtofen Doll", "t5_weapon_nesting_bomb_richtofen_c", image.Rect(171, 130, 341, 300)),
		// the characters of "Five", from its loading screen
		onScreen("jfk", "John F. Kennedy", "pentagon", 384, 148, 210),
		onScreen("mcnamara", "Robert McNamara", "pentagon", 145, 137, 210),
		onScreen("nixon", "Richard Nixon", "pentagon", 931, 444, 216),
		onScreen("castro", "Fidel Castro", "pentagon", 620, 233, 216),
		// the characters of Call of the Dead, from its poster, with George A. Romero's face behind them
		onScreen("trejo", "Danny Trejo", "coast", 305, 423, 137),
		onScreen("gellar", "Sarah Michelle Gellar", "coast", 387, 484, 125),
		onScreen("englund", "Robert Englund", "coast", 536, 324, 125),
		onScreen("rooker", "Michael Rooker", "coast", 384, 328, 125),
		onScreen("romero", "George A. Romero", "coast", 266, 273, 250),
	),
	group("Zombies",
		// the zombies in the pictures of the World at War maps in the map select
		inMap("nachtzombie", "Nacht Zombie", "loadscreen_zombie_prototype_mini", 86, 70, 120),
		inMap("nachthorde", "Nacht Horde", "loadscreen_zombie_prototype_mini", 172, 112, 100),
		inMap("verrucktzombie", "Verrückt Zombie", "loadscreen_zombie_asylum_mini", 78, 94, 112),
		inMap("verrucktzombie2", "Verrückt Zombie 2", "loadscreen_zombie_asylum_mini", 200, 150, 80),
		inMap("riesezombie", "Der Riese Zombie", "loadscreen_zombie_factory_mini", 62, 130, 112),
		inMap("riesezombie2", "Der Riese Zombie 2", "loadscreen_zombie_factory_mini", 196, 120, 90),
		inMap("riesezombie3", "Der Riese Zombie 3", "loadscreen_zombie_factory_mini", 70, 118, 70),
		// the zombies in the comics of the loading screens
		onScreen("comiczombie", "Comic Zombie", "theater", 182, 188, 188),
		onScreen("comiczombie2", "Comic Zombie 2", "theater", 429, 671, 296),
		onScreen("comiczombie3", "Comic Zombie 3", "theater", 426, 284, 171),
		onScreen("comiczombie4", "Comic Zombie 4", "theater", 154, 637, 296),
		onScreen("comichorde", "Comic Horde", "theater", 237, 262, 375),
		onScreen("horde", "The Horde", "prototype", 315, 307, 273),
		onScreen("screamer", "Screamer", "asylum", 390, 947, 148),
		onScreen("silhouettes", "Silhouettes", "asylum", 618, 927, 182),
		onScreen("greenzombie", "Green Zombie", "coast", 128, 632, 250),
		onScreen("posterzombies", "Poster Zombies", "coast", 742, 262, 296),
		onScreen("zombiehand", "Zombie Hand", "coast", 470, 785, 364),
		// the hellhound, from its calling card
		card("hellhound", "Hellhound", "emblem_bg_zombies05", 141),
	),
	// other parts of the maps' loading screens, which are pages of comics, map by map
	group("Comics",
		// Kino der Toten
		onScreen("crew", "The Crew", "theater", 291, 719, 171),
		onScreen("zzt", "ZZT!", "theater", 563, 506, 336),
		onScreen("clock", "Clock", "theater", 902, 535, 148),
		onScreen("years", "Years", "theater", 851, 671, 296),
		onScreen("soldier", "Soldier", "theater", 838, 319, 193),
		onScreen("ambush", "Ambush", "theater", 736, 341, 262),
		// "Five"
		onScreen("seal", "Presidential Seal", "pentagon", 886, 142, 262),
		// Ascension
		onScreen("scientist", "Scientist", "cosmodrome", 84, 228, 296),
		onScreen("rockets", "Rockets", "cosmodrome", 307, 262, 432),
		onScreen("device", "The Device", "cosmodrome", 415, 478, 125),
		onScreen("starchart", "Star Chart", "cosmodrome", 394, 757, 262),
		onScreen("centrifuge", "Centrifuge", "cosmodrome", 614, 478, 478),
		onScreen("sunburst", "Sunburst", "cosmodrome", 880, 296, 341),
		// Call of the Dead
		onScreen("callofthedead", "Call of the Dead", "coast", 672, 592, 592),
		onScreen("cast", "The Cast", "coast", 410, 455, 523),
		// Shangri-La
		onScreen("eclipse", "Eclipse", "temple", 678, 165, 193),
		onScreen("temple", "Temple", "temple", 749, 341, 341),
		onScreen("crumbling", "Crumbling Temple", "temple", 358, 546, 523),
		onScreen("lightning", "Lightning", "temple", 224, 478, 410),
		// Moon, whose left page has a comic's advertisements
		onScreen("colaad", "Speed Cola Ad", "moon", 443, 228, 142),
		onScreen("colabottle", "Speed Cola Bottle", "moon", 443, 438, 319),
		onScreen("papad", "Pack-a-Punch Ad", "moon", 294, 302, 102),
		onScreen("thunderad", "Thunder Gun Ad", "moon", 402, 677, 125),
		onScreen("gunmule", "Gun Mule", "moon", 288, 398, 114),
		onScreen("monkeyad", "Surprise Monkey", "moon", 182, 210, 108),
		onScreen("zombiecomedy", "Zombie Comedy", "moon", 102, 592, 68),
		onScreen("gammaspecs", "Gamma Ray Specs", "moon", 404, 745, 125),
		onScreen("spaceman", "Spaceman", "moon", 389, 870, 137),
		onScreen("freecash", "Free Cash", "moon", 122, 404, 125),
		onScreen("spaceice", "Space Ice", "moon", 363, 506, 125),
		onScreen("moonsurface", "Moon Surface", "moon", 800, 341, 341),
		onScreen("astronaut", "Astronaut", "moon", 736, 796, 330),
		// Nacht der Untoten
		onScreen("bulb", "Light Bulb", "prototype", 150, 137, 148),
		onScreen("shadow", "Glowing Eyes", "prototype", 148, 307, 250),
		onScreen("silence", "Silence", "prototype", 618, 85, 137),
		onScreen("scientists", "Scientists", "prototype", 634, 284, 284),
		onScreen("corridor", "Corridor", "prototype", 835, 210, 284),
		onScreen("audience", "The Audience", "prototype", 288, 694, 171),
		onScreen("zzz", "ZZZ!", "prototype", 499, 899, 228),
		onScreen("silhouette", "Silhouette", "prototype", 854, 683, 455),
		// Verrückt
		onScreen("powerswitch", "Power Switch", "asylum", 838, 290, 205),
		onScreen("electroshock", "Electro-Shock", "asylum", 746, 119, 193),
		// Shi No Numa
		onScreen("plant", "Overgrown Photo", "sumpf", 862, 98, 190),
		// Der Riese
		onScreen("group935", "Group 935", "factory", 266, 814, 273),
		onScreen("note", "Mainframe Note", "factory", 147, 210, 296),
		onScreen("mainframe", "Mainframe", "factory", 730, 472, 125),
		onScreen("blueprint", "Blueprint", "factory", 774, 193, 296),
		onScreen("vacuumtube", "Vacuum Tube", "factory", 922, 455, 364),
		onScreen("hexnut", "Hex Nut", "factory", 947, 108, 216),
	),
	group("Perks",
		// the perks' icons on the HUD, each a shield, like the power-ups' icons
		shield("juggernog", "Juggernog", "specialty_juggernaut_zombies"),
		shield("quickrevive", "Quick Revive", "specialty_quickrevive_zombies"),
		shield("speedcola", "Speed Cola", "specialty_fastreload_zombies"),
		shield("doubletap", "Double Tap", "specialty_doubletap_zombies"),
		shield("staminup", "Stamin-Up", "specialty_marathon_zombies"),
		shield("phdflopper", "PhD Flopper", "specialty_divetonuke_zombies"),
		shield("deadshot", "Deadshot Daiquiri", "specialty_ads_zombies"),
		shield("mulekick", "Mule Kick", "specialty_three_guns_zombies"),
		// the labels of the perks' bottles; Mule Kick's bottle has no label
		label("jugglabel", "Juggernog Label", "jugg"),
		label("revivelabel", "Quick Revive Label", "revive"),
		label("speedlabel", "Speed Cola Label", "sleight"),
		label("doubletaplabel", "Double Tap Label", "dbltap"),
		label("staminuplabel", "Stamin-Up Label", "marathon"),
		label("phdlabel", "PhD Flopper Label", "nuke"),
		label("deadshotlabel", "Deadshot Daiquiri Label", "ads"),
		// the machines, as sketched on Verrückt's loading screen
		onScreen("juggmachine", "Juggernog Machine", "asylum", 81, 393, 273),
		onScreen("revivemachine", "Quick Revive Machine", "asylum", 115, 814, 250),
		onScreen("speedmachine", "Speed Cola Machine", "asylum", 832, 546, 284),
		onScreen("doubletapmachine", "Double Tap Machine", "asylum", 208, 122, 228),
	),
	group("Power-ups",
		shield("instakill", "Insta-Kill", "specialty_instakill_zombies"),
		shield("doublepoints", "Double Points", "specialty_2x_zombies"),
		shield("firesale", "Fire Sale", "specialty_firesale_zombies"),
		shield("bonfiresale", "Bonfire Sale", "zom_icon_bonfire"),
		shield("bullets", "Bullets", "zom_icon_bullets"),
	),
	group("Items",
		icon("revive", "Revive", "waypoint_revive"),
		icon("syrette", "Syrette", "hud_syrette"),
		icon("stopwatch", "Stopwatch", "zombie_stopwatch"),
		icon("reel", "Film Reel", "zom_icon_theater_reel"),
		icon("meteorite", "Meteorite", "zom_hud_icon_meteor"),
		icon("dynamite", "Dynamite", "zom_hud_icon_dynamite"),
		icon("vril", "Vril Device", "zom_hud_icon_vril"),
		icon("fuse", "Fuse", "zom_hud_icon_fuse"),
		icon("bottle", "Bottle", "zom_hud_icon_bottle"),
		icon("hacker", "Hacker", "zom_hud_icon_hacker"),
		// the mystery box's bear, from its calling card
		card("teddy", "Teddy Bear", "emblem_bg_teddy_bear", 159),
	),
	// The weapons a player gets other than from a wall, mostly from the box and the power-ups. They are the weapons
	// of the Zombies maps that the game files have an icon of. The icon is the kill feed's, which most of the wonder
	// weapons lack. Each icon is the one its weapon has in the maps' zones.
	group("Weapons",
		icon("thundergun", "Thundergun", "hud_thundergun"),
		icon("deathmachine", "Death Machine", "hud_ks_minigun"),
		icon("scavenger", "Scavenger Bolt", "hud_indicator_sniper_explosive"),
		icon("monkeybomb", "Monkey Bomb", "hud_cymbal_monkey"),
		icon("gersch", "Gersch Device", "hud_blackhole"),
		icon("qed", "QED", "hud_quantum_bomb"),
		icon("dolls", "Matryoshka Dolls", "hud_nestingbomb"),
		icon("crossbow", "Crossbow", "hud_crossbow"),
		icon("ballisticknife", "Ballistic Knife", "hud_ballistic_knife"),
		icon("m1911", "M1911", "hud_colt"),
		icon("python", "Python", "hud_python"),
		icon("cz75", "CZ75", "hud_cz75"),
		icon("spectre", "Spectre", "hud_spectre"),
		icon("spas12", "SPAS-12", "hud_spas12"),
		icon("hs10", "HS-10", "hud_hs10"),
		icon("famas", "FAMAS", "hud_famas"),
		icon("aug", "AUG", "hud_aug"),
		icon("galil", "Galil", "hud_galil"),
		icon("commando", "Commando", "hud_commando"),
		icon("fnfal", "FN FAL", "hud_fnfal"),
		icon("g11", "G11", "hud_g11"),
		icon("hk21", "HK21", "hud_hk21"),
		icon("rpk", "RPK", "hud_rpk"),
		icon("dragunov", "Dragunov", "hud_dragunovsvd"),
		icon("l96a1", "L96A1", "hud_l96"),
		icon("chinalake", "China Lake", "hud_china_lake"),
		icon("law", "M72 LAW", "hud_law"),
	),
	// the weapons a wall sells, as their chalk outlines on the wall
	group("Wall weapons",
		chalk("m14", "M14", "chalk_weapon_m14_c"),
		chalk("olympia", "Olympia", "chalk_weapon_rottweil72_c"),
		chalk("mpl", "MPL", "chalk_weapon_mpl_c"),
		chalk("pm63", "PM63", "chalk_weapon_pm63_c"),
		chalk("mp5k", "MP5K", "chalk_weapon_mp5_c"),
		chalk("ak74u", "AK74u", "chalk_weapon_ak74u_c"),
		chalk("m16", "M16", "chalk_weapon_m16_c"),
		chalk("stakeout", "Stakeout", "chalk_weapon_ithaca_c"),
		chalk("mp40", "MP40", "chalk_weapon_mp40_c"),
		chalk("bowie", "Bowie Knife", "chalk_weapon_bowie_knife"),
		chalk("sickle", "Sickle", "chalk_weapon_sickle"),
		// one texture has the Claymore and the grenades, side by side
		chalk("claymore", "Claymore", "chalk_weapon_explosives_c").of(image.Rect(0, 0, 256, 256)),
		chalk("grenades", "Grenades", "chalk_weapon_explosives_c").of(image.Rect(256, 0, 512, 256)),
		// the wall weapons of World at War
		chalk("kar98k", "Kar98k", "k98_chalk_c"),
		chalk("m1carbine", "M1A1 Carbine", "m1carbine_chalk_c"),
		chalk("m1garand", "M1 Garand", "m1garand_chalk_c"),
		chalk("gewehr43", "Gewehr 43", "g43_chalk_c"),
		chalk("arisaka", "Arisaka", "type99_rifle_chalk_c"),
		chalk("thompson", "Thompson", "tommy_chalk_c"),
		chalk("type100", "Type 100", "type100_smg_chalk_c"),
		chalk("stg44", "STG-44", "stg44_chalk_c"),
		chalk("fg42", "FG42", "fg42_chalk_c"),
		chalk("bar", "BAR", "bar_chalk_c"),
		chalk("doublebarrel", "Double-Barreled Shotgun", "db_shotgun_chalk_c"),
		chalk("sawedoff", "Sawed-Off Shotgun", "sawedoff_chalk_c"),
		chalk("trenchgun", "Trench Gun", "trenchgun_chalk_c"),
		chalk("betty", "Bouncing Betty", "bouncing_betty_chalk_c"),
	),
	// other chalk drawings on the maps' walls; a texture with two drawings makes two pictures
	group("Chalk",
		chalk("tally1", "Round 1", "chalkmarks_1"),
		chalk("tally2", "Round 2", "chalkmarks_2"),
		chalk("tally3", "Round 3", "chalkmarks_3"),
		chalk("tally4", "Round 4", "chalkmarks_4"),
		chalk("tally5", "Round 5", "chalkmarks_5"),
		// the signs to the Pack-a-Punch machine (the machine, without the arrow beside it) and to the power
		chalk("packapunch", "Pack-a-Punch", "chalk_writing_powerpack").of(image.Rect(206, 0, 512, 256)),
		chalk("power", "Power", "chalk_writing_powerpack").of(image.Rect(0, 256, 512, 512)),
		// the sign to the mystery box, over an arrow like the one to the power
		chalk("mysterybox", "Mystery Box", "lightningbox_chalk_c").of(image.Rect(0, 0, 512, 262)),
		chalk("unknown", "Question Mark", "unknown_chalk_c"),
		chalk("help", "Help", "help_chalk_c"),
		// the 115 under Shi No Numa's "Look up to see the light", and the hut beside it
		chalk("element115", "115", "sumpf_guide_115_chalk_c").of(image.Rect(40, 380, 360, 512)),
		chalk("hut", "Hut", "sumpf_guide_115_chalk_c").of(image.Rect(250, 150, 512, 400)),
	),
)

// group returns the pictures with their group set to name.
func group(name string, pictures ...picture) []picture {
	for i := range pictures {
		pictures[i].group = name
	}
	return pictures
}

// icon, shield and chalk each return a picture of that kind with the given id and name, made from
// images/<texture>.iwi.
func icon(id, name, texture string) picture {
	return picture{id: id, name: name, image: texture, kind: "icon"}
}

func shield(id, name, texture string) picture {
	return picture{id: id, name: name, image: texture, kind: "shield"}
}

func chalk(id, name, texture string) picture {
	return picture{id: id, name: name, image: texture, kind: "chalk"}
}

// crop returns a picture that is the given part of its texture, in texels.
func crop(id, name, texture string, part image.Rectangle) picture {
	return picture{id: id, name: name, image: texture, kind: "crop", part: part}
}

// card returns a picture from a calling card, which is a strip of 256x64. The picture is a crop of the strip: the
// square as high as the strip, centred on the texel x of the strip's width.
func card(id, name, texture string, x int) picture {
	return crop(id, name, texture, image.Rect(x-32, 0, x+32, 64))
}

// label returns a picture of the label of a perk's bottle. Every bottle's texture has its label in the same place
// (images/~-gzombie_perkbottle_<bottle>_c.iwi). The label is round, and it is drawn higher than wide to allow for the
// bottle's curve. The picture is a close crop of the label's upper left: the arc of the perk's name over its mark,
// off the middle.
func label(id, name, bottle string) picture {
	return crop(id, name, "~-gzombie_perkbottle_"+bottle+"_c", image.Rect(113, 235, 265, 405))
}

// squeezed returns a crop of a picture that the game stores squeezed. The game stores a picture that it draws wide
// in a square texture, so a square of the drawn picture is narrower than high in the texture, by the same ratio. The
// crop is centred on the texel x, y and is h texels high. It is across texels wide for every down texels high: 3 for
// every 4 in a map's picture in the map select (inMap), and 9 for every 16 in a map's loading screen (onScreen, made
// from images/loadscreen_zombie_<screen>.iwi).
func squeezed(id, name, texture string, x, y, h, across, down int) picture {
	w := h * across / down
	return crop(id, name, texture, image.Rect(x-w/2, y-h/2, x-w/2+w, y-h/2+h))
}

func inMap(id, name, texture string, x, y, h int) picture {
	return squeezed(id, name, texture, x, y, h, 3, 4)
}

func onScreen(id, name, screen string, x, y, h int) picture {
	return squeezed(id, name, "loadscreen_zombie_"+screen, x, y, h, 9, 16)
}

// of returns the picture made from only the given part of its texture, in texels.
func (p picture) of(part image.Rectangle) picture {
	p.part = part
	return p
}

// mapPictures returns the pictures of the maps that the page lists. A map's name is the id of its picture, and the
// map's title is the picture's name.
func mapPictures() []picture {
	var set []picture
	for _, m := range mapTitles {
		if m.preview != "" { // the server reads no pictures of T6's maps
			set = append(set, picture{id: m.name, name: m.title, image: m.preview, kind: "map"})
		}
	}
	return set
}

// pictureIndex has the index of each picture in pictureSet, by the picture's id.
var pictureIndex = func() map[string]int {
	index := make(map[string]int, len(pictureSet))
	for i, p := range pictureSet {
		index[p.id] = i
	}
	return index
}()

func pictureByID(id string) (picture, bool) {
	i, ok := pictureIndex[id]
	if !ok {
		return picture{}, false
	}
	return pictureSet[i], true
}

// ---- the web side (web): the pictures, made from the game files ----

// playerPicture returns the picture of the set with the given id, in the form the page shows (above), as a PNG. The
// picture is made once and kept. playerPicture returns false when the set has no such picture, or when the game files
// lack its image.
func (h *web) playerPicture(id string) (mapPicture, bool) {
	p, ok := pictureByID(id)
	if !ok {
		return mapPicture{}, false
	}
	return h.pictureFrom(p, h.iwdImage)
}

// pictureFrom returns p's picture (playerPicture). It reads the picture's texture by calling texture, with h.art held.
func (h *web) pictureFrom(p picture, texture func(name string) (*image.NRGBA, error)) (mapPicture, bool) {
	return h.kept("picture/"+p.id, "the players' picture "+p.id, func() (mapPicture, error) {
		img, err := texture(p.image)
		if err != nil {
			return mapPicture{}, err
		}
		return pngOf(framed(img, p))
	})
}

// playerPictures returns the ids of the pictures of the set that the server could make from the game files, in the
// set's order. It makes the pictures now. That reads the archives, which waits for the game files to be hashed. The
// server calls it once, when it starts, and the lobby keeps the result (lobby.havePictures). The pictures are made
// texture by texture. A texture is read and decoded once for all the pictures made from it (a loading screen has a
// dozen), and is kept only until the next texture is read.
func (h *web) playerPictures() []string {
	var last struct {
		name string
		img  *image.NRGBA
		err  error
	}
	texture := func(name string) (*image.NRGBA, error) {
		if name != last.name {
			last.name = name
			last.img, last.err = h.iwdImage(name)
		}
		return last.img, last.err
	}
	byTexture := slices.Clone(pictureSet)
	slices.SortStableFunc(byTexture, func(a, b picture) int { return strings.Compare(a.image, b.image) })
	made := map[string]bool{}
	for _, p := range byTexture {
		_, made[p.id] = h.pictureFrom(p, texture)
	}
	have := []string{}
	for _, p := range pictureSet {
		if made[p.id] {
			have = append(have, p.id)
		}
	}
	return have
}

const (
	pictureSide = 128 // a picture's width and height in pixels, as the page loads it
	// The page shows a picture in a circle. An icon is as large as keeps its corners inside a circle of pictureRound
	// pixels across (fitted). Its longer side is at least pictureInner, which is the size of an icon as high as wide:
	// such an icon has little in its corners. Its longer side is at most pictureWide, which is the size of a long
	// icon, such as a rifle's.
	pictureRound = 124
	pictureInner = 96
	pictureWide  = 112
	// pictureShows is the alpha, out of 255, above which a texel of an icon or a chalk drawing shows. A texel with
	// less is the texture's noise, and is not part of what is fitted in the square.
	pictureShows = 16
	// pictureChalkGain is the factor by which chalk is made stronger than it is in its texture, after it is scaled. A
	// weapon's outline has lines a texel or two wide that are not white. A pixel of the picture covers four or five
	// texels, so the lines are faint in the picture.
	pictureChalkGain = 3
)

// pictureCream is the page's cream (web/src/page.css: --fg), the colour the chalk is drawn in.
var pictureCream = color.NRGBA{0xec, 0xe7, 0xdf, 0xff}

// framed returns a picture of the set in the form the page shows, 128x128. It makes the picture from the texture
// according to the picture's kind (above).
func framed(img *image.NRGBA, p picture) *image.NRGBA {
	// b is the part of the texture the picture is made from. It is the whole texture when the picture names no part,
	// or names a part that is outside the texture.
	b := img.Bounds()
	if part := p.part.Intersect(b); !part.Empty() {
		b = part
	}
	switch p.kind {
	case "map":
		// The texture has a few empty rows of texels above and below the picture (its frame, with rounded corners).
		// The picture is the rows between them, which are found where the middle column is opaque.
		mid, top, bottom := b.Min.X+b.Dx()/2, b.Min.Y, b.Max.Y
		for top < bottom && img.NRGBAAt(mid, top).A < 255 {
			top++
		}
		for bottom > top && img.NRGBAAt(mid, bottom-1).A < 255 {
			bottom--
		}
		if top == bottom {
			top, bottom = b.Min.Y, b.Max.Y
		}
		// The game draws the texture at 4:3 and stores it square, so a square of the drawn picture is three quarters
		// as wide in the texture as it is high.
		w := max((bottom-top)*3*b.Dx()/(4*b.Dy()), 1)
		x := b.Min.X + (b.Dx()-w)/2
		return scaled(img, image.Rect(x, top, x+w, bottom), pictureSide, pictureSide)
	case "crop":
		return scaled(img, b, pictureSide, pictureSide)
	case "chalk":
		// drawn is a picture in one colour, the cream. Its alpha at each texel is the amount of chalk there.
		drawn := image.NewNRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := img.NRGBAAt(x, y)
				there := int(max(c.R, c.G, c.B)) * int(c.A) / 255
				drawn.SetNRGBA(x, y, color.NRGBA{pictureCream.R, pictureCream.G, pictureCream.B, uint8(there)})
			}
		}
		out := fitted(drawn, shown(drawn, b), false)
		for i := 0; i < len(out.Pix); i += 4 {
			// Set the colour to the cream again. Scaling weighs the colour by its alpha, which rounds it where there
			// is little chalk.
			out.Pix[i], out.Pix[i+1], out.Pix[i+2] = pictureCream.R, pictureCream.G, pictureCream.B
			out.Pix[i+3] = uint8(min(int(out.Pix[i+3])*pictureChalkGain, 255))
		}
		return out
	}
	return fitted(img, shown(img, b), p.kind == "shield")
}

// shown returns the part of a picture that shows, within b: the bounding box of the texels whose alpha is above
// pictureShows. It returns all of b when there is no such texel.
func shown(img *image.NRGBA, b image.Rectangle) image.Rectangle {
	box := image.Rectangle{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A > pictureShows {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if box.Empty() {
		return b
	}
	return box
}

// fitted returns the part r of a picture, framed like an icon: in its own shape, in the middle of the square, with
// nothing around it, and as large as fits the circle that the page shows of the square. Its longer side is the
// length that keeps its corners inside a circle of pictureRound across, but at least pictureInner and at most
// pictureWide. That is 96 for a part as high as wide, 110 for one twice as long as high, and 112 for one three times
// as long. The middle of the part's box is put in the middle of the square. For a shield (byWeight), the middle of
// its weight is put there instead (weightOf). The shield is then as far off the middle as the middle of its weight is
// off the middle of its box, but it never goes past the square's edge.
func fitted(img *image.NRGBA, r image.Rectangle, byWeight bool) *image.NRGBA {
	long, short := max(r.Dx(), r.Dy()), min(r.Dx(), r.Dy())
	side := int(pictureRound / math.Hypot(1, float64(short)/float64(long)))
	side = min(max(side, pictureInner), pictureWide)
	w, h := side, side
	if r.Dx() > r.Dy() {
		h = max(side*r.Dy()/r.Dx(), 1)
	} else {
		w = max(side*r.Dx()/r.Dy(), 1)
	}
	part := scaled(img, r, w, h)
	left, top := (pictureSide-w)/2, (pictureSide-h)/2
	if byWeight {
		x, y := weightOf(part)
		left = min(max(left+int(math.Round(float64(w)/2-x)), 0), pictureSide-w)
		top = min(max(top+int(math.Round(float64(h)/2-y)), 0), pictureSide-h)
	}
	out := image.NewNRGBA(image.Rect(0, 0, pictureSide, pictureSide))
	for y := 0; y < h; y++ {
		copy(out.Pix[out.PixOffset(left, top+y):], part.Pix[y*part.Stride:y*part.Stride+w*4])
	}
	return out
}

// weightOf returns the middle of a picture's weight, measured from the picture's top left corner. It is the point
// where the picture would balance if each pixel weighed as much as its alpha. It is the middle of the picture when
// every pixel is transparent.
func weightOf(img *image.NRGBA) (float64, float64) {
	b := img.Bounds()
	var across, down, all float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			there := float64(img.NRGBAAt(x, y).A)
			across, down, all = across+there*(float64(x-b.Min.X)+0.5), down+there*(float64(y-b.Min.Y)+0.5), all+there
		}
	}
	if all == 0 {
		return float64(b.Dx()) / 2, float64(b.Dy()) / 2
	}
	return across / all, down / all
}

// scaled returns the part r of a picture, scaled to w by h. Each texel's colour is weighed by its alpha. The colour
// that the texture has where it is transparent is not part of the picture, and would otherwise show around an icon's
// edge. Along a side that shrinks to less than half, each pixel is the average of the texels it covers (averaged).
// resize alone would blend the four texels a pixel falls between and skip the rest, and so lose the thin lines of a
// chalk drawing. resize does the scaling that is left.
func scaled(in *image.NRGBA, r image.Rectangle, w, h int) *image.NRGBA {
	weighed := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		from, to := in.Pix[in.PixOffset(r.Min.X, r.Min.Y+y):], weighed.Pix[y*weighed.Stride:]
		for i := 0; i < r.Dx()*4; i += 4 {
			a := int(from[i+3])
			for c := 0; c < 3; c++ {
				to[i+c] = uint8((int(from[i+c])*a + 127) / 255)
			}
			to[i+3] = from[i+3]
		}
	}
	aw, ah := r.Dx(), r.Dy()
	if aw > 2*w {
		aw = w
	}
	if ah > 2*h {
		ah = h
	}
	out := resize(averaged(weighed, aw, ah), w, h)
	for i := 0; i < len(out.Pix); i += 4 {
		if a := int(out.Pix[i+3]); a > 0 {
			for c := 0; c < 3; c++ {
				out.Pix[i+c] = uint8(min((int(out.Pix[i+c])*255+a/2)/a, 255))
			}
		}
	}
	return out
}

// averaged shrinks an image to w by h. Neither may be more than the image's own size. Each pixel is the average of
// the area of the image that it covers. A pixel of the image that is covered in part counts for that part.
func averaged(in *image.NRGBA, w, h int) *image.NRGBA {
	b := in.Bounds()
	if w == b.Dx() && h == b.Dy() {
		return in
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	across, down := float64(b.Dx())/float64(w), float64(b.Dy())/float64(h)
	for y := 0; y < h; y++ {
		top, bottom := float64(y)*down, float64(y+1)*down
		for x := 0; x < w; x++ {
			left, right := float64(x)*across, float64(x+1)*across
			var sum [4]float64
			covered := 0.0
			for v := int(top); v < b.Dy() && float64(v) < bottom; v++ {
				rows := min(float64(v+1), bottom) - max(float64(v), top)
				for u := int(left); u < b.Dx() && float64(u) < right; u++ {
					part := rows * (min(float64(u+1), right) - max(float64(u), left))
					for c := range sum {
						sum[c] += float64(in.Pix[v*in.Stride+u*4+c]) * part
					}
					covered += part
				}
			}
			for c := range sum {
				out.Pix[y*out.Stride+x*4+c] = uint8(sum[c]/covered + 0.5)
			}
		}
	}
	return out
}

// servePlayerPicture answers GET /pictures/<id>.png (above), or with a 404.
func (h *web) servePlayerPicture(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/pictures/"), ".png")
	pic, found := h.playerPicture(id)
	if !ok || !found {
		http.NotFound(w, r)
		return
	}
	h.serveKept(w, r, pic)
}

// ---- the lobby's side (lobby): which player has which picture ----

// picturesPickedOnly are the groups whose pictures a player can pick but the lobby never gives (givePicture). A
// rifle's icon or outline is a thin line across a round picture, which shows little next to a name, and these groups
// are three in ten of the set.
var picturesPickedOnly = []string{"Weapons", "Wall weapons"}

// setPictures sets the pictures of the set that the lobby has. It stores their ids, their entries for the page
// (view), and the ids of the pictures that the lobby gives (givePicture). The lobby gives all of them except those of
// the groups in picturesPickedOnly, unless it has no others. The caller holds l.mu.
func (l *lobby) setPictures(ids []string) {
	l.pictures, l.picturesShown, l.picturesGiven = []string{}, []pictureView{}, nil
	for _, id := range ids {
		pic, ok := pictureByID(id)
		if !ok {
			continue
		}
		l.pictures = append(l.pictures, id)
		l.picturesShown = append(l.picturesShown, pictureView{pic.id, pic.name, pic.group})
		if !slices.Contains(picturesPickedOnly, pic.group) {
			l.picturesGiven = append(l.picturesGiven, id)
		}
	}
	if len(l.picturesGiven) == 0 {
		l.picturesGiven = l.pictures // the lobby has only the weapons
	}
	l.picturesMade++
}

// givePicture gives p a picture of the set when p needs one, and reports whether p's picture changed. The caller
// holds l.mu. A player needs a picture when they have none, or have one that is no longer in the set, and they have
// no avatar on Discord, which would be their picture (avatar). The picture is a random one of those the lobby gives
// (setPictures) that no player has yet. Once every one of those is taken, it is a random one of them all. While the
// lobby has no pictures (the server has not made them yet, or the game files lack them all), no one is given a
// picture, and each player keeps the picture they had for when the lobby has pictures. A player also keeps a
// picture that the lobby lacks while it has others (the server couldn't read an archive when it started, or the game
// files lack the image). The picture is still theirs and shows again once the lobby has it. Until then another
// picture shows in its place (pictureShown).
func (l *lobby) givePicture(p *player) bool {
	return l.givePictureBut(p, nil)
}

// givePictureBut is givePicture with taken, the pictures that players already have (picturesTaken). havePictures
// passes taken so that it is worked out once for all the players. With nil, givePictureBut works it out itself.
func (l *lobby) givePictureBut(p *player, taken map[string]bool) bool {
	was := p.picture
	if len(l.pictures) == 0 {
		return false
	}
	if _, ok := pictureByID(p.picture); !ok {
		p.picture, p.picked = "", false
	}
	if p.picture == "" && p.discordAvatar() == "" {
		if taken == nil {
			taken = l.picturesTaken()
		}
		free := slices.DeleteFunc(slices.Clone(l.picturesGiven), func(id string) bool { return taken[id] })
		if len(free) == 0 {
			free = l.picturesGiven
		}
		p.picture = free[rand.IntN(len(free))]
		taken[p.picture] = true
	}
	return p.picture != was
}

// picturesTaken returns the pictures of the set that a player has. The caller holds l.mu.
func (l *lobby) picturesTaken() map[string]bool {
	taken := make(map[string]bool, len(l.players))
	for _, p := range l.players {
		taken[p.picture] = true
	}
	return taken
}

// pictureShown returns the id of the picture of the set that shows for p. The caller holds l.mu. It returns "" when p
// has no picture. It also returns "" when p's avatar on Discord is their picture (avatar): p picked no picture
// themselves, or the lobby doesn't have p's picture and p has an avatar. The lobby has no pictures until the server
// has made them (havePictures). After that, a player whose picture the lobby lacks, and who has no avatar, is shown
// another picture in its place. That picture is one of those the lobby gives, and it is the same one each time. The
// player's own picture stays as it was (givePicture).
func (l *lobby) pictureShown(p *player) string {
	switch {
	case !p.picked && p.discordAvatar() != "":
		return ""
	case slices.Contains(l.pictures, p.picture):
		return p.picture
	case p.picture == "" || p.discordAvatar() != "" || len(l.picturesGiven) == 0:
		return ""
	}
	of := fnv.New32a()
	of.Write([]byte(p.id))
	return l.picturesGiven[of.Sum32()%uint32(len(l.picturesGiven))]
}

// havePictures gives the lobby the pictures of the set that the server made (web.playerPictures). It then gives a
// picture to each player who needs one (givePicture). Those are the players that the players file has from before
// there were pictures, the players whose picture is no longer in the set, and the players whose page sent its hello
// before the pictures were made. The first two need one even if they never come back, because the leaderboards show
// them. The open pages are notified.
func (l *lobby) havePictures(ids []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.setPictures(ids)
	taken := l.picturesTaken()
	for _, p := range l.players {
		if l.givePictureBut(p, taken) {
			l.save()
		}
	}
	l.change()
}

// setPicture makes the picture of the set with the given id p's picture, as p's own pick. It must be a picture that
// the lobby has. With "", p picks none and p's avatar on Discord is their picture again, so p must have an avatar.
// The caller holds l.mu.
func (l *lobby) setPicture(p *player, id string) error {
	switch {
	case id == "" && p.discordAvatar() == "":
		return errors.New("You have no avatar on Discord.")
	case id != "" && !slices.Contains(l.pictures, id):
		return fmt.Errorf("There is no picture %s.", id)
	}
	p.picture, p.picked = id, id != ""
	l.save()
	l.change()
	return nil
}
