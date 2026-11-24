// e2e/game/bo2/steer.gsc - the game tests' (bo2/*.spec.ts), never the server's: a script that a test puts in its
// server's Black Ops II games (t6.ts: steer), so that it can steer a game whose players stand still, from the game's
// console. The zombies stay away from the start, so the players go down only when the test says. Each command is a
// dvar that the script reads and clears:
//
//   set steer_down A|B|all   downs every player who is up: of that team of Grief's, or all of them
//   set steer_clear 1        ends the round: none of its zombies are left, nor still to come
//   set steer_cure <name>    moves the player onto Turned's cure, which they pick up as a player does
//   set steer_timeup 1       runs Turned's clock out
//   set steer_report 1       prints a line of each player's:
//                            zombies_steer: <name> team <A|B|none> <up|down> <human|zombie> score <points>
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
}
