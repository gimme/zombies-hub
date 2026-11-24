// e2e/game/bo2/steer.gsc - the game tests' (bo2/*.spec.ts), never the server's: a script that a test puts in its
// server's Black Ops II games (t6.ts: steer), so that it can steer a game whose players stand still, from the game's
// console. The zombies stay away from the start, so the players go down only when the test says. Each command is a
// dvar that the script reads and clears:
//
//   set steer_down A|B|all   downs every player who is up: of that team of Grief's, or all of them
//   set steer_clear 1        ends the round: none of its zombies are left, nor still to come
//   set steer_cure <name>    moves the player onto Turned's cure, which they pick up as a player does
//   set steer_timeup 1       runs Turned's clock out
//   set steer_limit 1        prints how many zombies the level lets be alive at once:
//                            zombies_steer: the zombies at once are <n>
//   set steer_capture 1      on Origins, has the map set its limit as it does when a generator's capture starts or
//                            ends, and prints the limit at once, before anything else runs:
//                            zombies_steer: a capture leaves the round <n> zombies at once
//   set steer_wall <name>    gives the player the gun of the wall buy nearest to them, and moves every player to that
//                            wall buy, where the level decides who sees its prompt, and nothing downs them:
//                            zombies_steer: <name> has <weapon> off the wall
//   set steer_report 1       prints a line of each player's, then how many they are, and whether the level has
//                            started, past its black screen at the start:
//                            zombies_steer: <name> team <A|B|none> <up|down> <human|zombie> score <points>
//                            zombies_steer: reported <n> players, the level <started|waits>
//
// The rest is the game's own: who is in which team, who wins Grief and Turned, when it is game over.
init()
{
	level thread steer_watch();
}

// The dvar's value, which it then clears: "" when it has none.
steer_take( dvar )
{
	value = getdvar( dvar );

	if ( value != "" )
		setdvar( dvar, "" );

	return value;
}

steer_watch()
{
	level endon( "end_game" );

	for ( ;; )
	{
		wait 0.25;

		// the round's zombies spawn while the flag is set (maps/mp/zombies/_zm.gsc: round_spawning)
		if ( isdefined( level.flag ) && isdefined( level.flag["spawn_zombies"] ) && level.flag["spawn_zombies"] )
			common_scripts\utility::flag_clear( "spawn_zombies" );

		// each command in a thread of its own: an error in one ends that thread, not this one
		down = steer_take( "steer_down" );

		if ( down != "" )
			level thread steer_down( down );

		if ( steer_take( "steer_clear" ) != "" )
			level thread steer_clear();

		cure = steer_take( "steer_cure" );

		if ( cure != "" )
			level thread steer_cure( cure );

		if ( steer_take( "steer_timeup" ) != "" )
		{
			// what Turned's clock does as it runs out (maps/mp/gametypes_zm/zcleansed.gsc: cleansedontimelimit)
			level notify( "normal_game_end" );
			printf( "zombies_steer: the clock ran out" );
		}

		if ( steer_take( "steer_limit" ) != "" )
			printf( "zombies_steer: the zombies at once are " + level.zombie_ai_limit );

		if ( steer_take( "steer_capture" ) != "" )
			level thread steer_capture();

		wall = steer_take( "steer_wall" );

		if ( wall != "" )
			level thread steer_wall( wall );

		if ( steer_take( "steer_report" ) != "" )
			level thread steer_report();
	}
}

steer_clear()
{
	level.zombie_total = 0;
	zombies = getaiarray( level.zombie_team );

	for ( i = 0; i < zombies.size; i++ )
		zombies[i] dodamage( zombies[i].health + 1000, zombies[i].origin );

	printf( "zombies_steer: the round is clear of " + zombies.size + " zombies" );
}

steer_down( team )
{
	players = getplayers();
	n = 0;

	for ( i = 0; i < players.size; i++ )
	{
		player = players[i];

		if ( team != "all" && ( !isdefined( player._encounters_team ) || player._encounters_team != team ) )
			continue;

		if ( player.sessionstate != "playing" || player maps\mp\zombies\_zm_laststand::player_is_in_laststand() )
			continue;

		player disableinvulnerability(); // which steer_wall gave them
		player dodamage( player.health + 1000, player.origin );
		n++;
	}

	printf( "zombies_steer: downed " + n + " of " + team );
}

steer_cure( name )
{
	players = getplayers();

	for ( i = 0; i < players.size; i++ )
	{
		if ( tolower( players[i].name ) != tolower( name ) || !isdefined( level.active_powerups ) )
			continue;

		for ( j = 0; j < level.active_powerups.size; j++ )
		{
			cure = level.active_powerups[j];

			if ( isdefined( cure ) && isdefined( cure.powerup_name ) && cure.powerup_name == "the_cure" )
			{
				players[i] setorigin( cure.origin );
				printf( "zombies_steer: " + players[i].name + " is at the cure" );
				return;
			}
		}
	}

	printf( "zombies_steer: no cure for " + name );
}

// The map's function is by name, as only Origins has its script (maps/mp/zm_tomb_capture_zones.gsc). It waits only
// while more zombies are alive than its limit, and none are.
steer_capture()
{
	limit = GetFunction( "maps/mp/zm_tomb_capture_zones", "capture_event_handle_ai_limit" );
	[[ limit ]]();
	printf( "zombies_steer: a capture leaves the round " + level.zombie_ai_limit + " zombies at once" );
}

// A place to stand at the wall buy of the stub, on one of its sides, where the players may be: in a zone of the map
// that is open (a player elsewhere dies at once: maps/mp/zombies/_zm.gsc: player_out_of_playable_area_monitor), and
// in reach of the wall buy's trigger (_zm_unitrigger.gsc: get_closest_unitriggers).
steer_wall_place( stub )
{
	sides = [];
	sides[0] = anglestoright( stub.angles );
	sides[1] = anglestoforward( stub.angles );

	for ( i = 0; i < sides.size; i++ )
	{
		for ( way = -1; way <= 1; way += 2 )
		{
			place = stub.origin + sides[i] * ( 24 * way ) - ( 0, 0, 40 );

			if ( maps\mp\zombies\_zm_utility::check_point_in_enabled_zone( place ) )
				return place;
		}
	}

	return undefined;
}

steer_wall( name )
{
	players = getplayers();
	player = undefined;

	for ( i = 0; i < players.size; i++ )
	{
		if ( tolower( players[i].name ) == tolower( name ) )
			player = players[i];
	}

	// the wall buys of the zones that the players are in or next to, each the stub of a trigger
	// (maps/mp/zombies/_zm_weapons.gsc: init_spawnable_weapon_upgrade, _zm_unitrigger.gsc: register_static_unitrigger)
	wall = undefined;
	at = undefined;

	for ( i = 0; isdefined( player ) && isdefined( level.active_zone_names ) && i < level.active_zone_names.size; i++ )
	{
		stubs = level.zones[level.active_zone_names[i]].unitrigger_stubs;

		for ( j = 0; isdefined( stubs ) && j < stubs.size; j++ )
		{
			if ( !isdefined( stubs[j].zombie_weapon_upgrade ) || weapontype( stubs[j].zombie_weapon_upgrade ) != "bullet" )
				continue;

			if ( isdefined( wall ) && distancesquared( player.origin, stubs[j].origin ) >= distancesquared( player.origin, wall.origin ) )
				continue;

			place = steer_wall_place( stubs[j] );

			if ( isdefined( place ) )
			{
				wall = stubs[j];
				at = place;
			}
		}
	}

	if ( !isdefined( wall ) )
	{
		printf( "zombies_steer: no wall buy for " + name );
		return;
	}

	player maps\mp\zombies\_zm_weapons::weapon_give( wall.zombie_weapon_upgrade );

	// Nothing downs the players there until the test does (steer_down): the level kills a player whom it finds outside
	// the map (maps/mp/zombies/_zm.gsc: player_out_of_playable_area_monitor), and TranZit's lava burns.
	for ( i = 0; i < players.size; i++ )
	{
		players[i] notify( "stop_player_out_of_playable_area_monitor" );
		players[i] enableinvulnerability();
		players[i] setorigin( at );
	}

	printf( "zombies_steer: " + player.name + " has " + wall.zombie_weapon_upgrade + " off the wall" );
}

steer_report()
{
	players = getplayers();

	for ( i = 0; i < players.size; i++ )
	{
		player = players[i];
		team = "none";
		state = "up";
		kind = "human";
		score = 0;

		if ( isdefined( player._encounters_team ) )
			team = player._encounters_team;

		if ( isdefined( player.score ) )
			score = player.score;

		if ( player.sessionstate != "playing" || player maps\mp\zombies\_zm_laststand::player_is_in_laststand() )
			state = "down";

		if ( isdefined( player.is_zombie ) && player.is_zombie )
			kind = "zombie";

		printf( "zombies_steer: " + player.name + " team " + team + " " + state + " " + kind + " score " + score );
	}

	// The level starts as its black screen at the start passes: Grief watches its teams from then on
	// (maps/mp/gametypes_zm/zgrief.gsc: zgrief_main).
	started = "waits";
	passed = "initial_blackscreen_passed";

	if ( isdefined( level.flag ) && isdefined( level.flag[passed] ) && level.flag[passed] )
		started = "started";

	printf( "zombies_steer: reported " + players.size + " players, the level " + started );
}
