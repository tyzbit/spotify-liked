# spotify-liked

Checks if the current song is liked or not because it can't be read via AppleScript.
It simply outputs a `false` or `true`.

\*\*NOTE: It will open an auth link in your browser briefly, but it will close.
It does this every hour.

I use this in BetterTouchTool to make a Stream Deck button that shows whether
the current song is liked or not.

## Unliked song

![Unliked song](unliked-song.png)

## Liked song

![Liked song](liked-song.png)

Set SPOTIFY_ID and SPOTIFY_SECRET in `.env`, you'll get those values when you
set up an appplication on the [Spotify Developer portal](https://developer.spotify.com/dashboard).

If you haven't already, you'll need to install the BetterTouchTool plugin by
going to `Settings -> Stream Deck -> Install/Reinstall Stream Deck Plugin`.
The config for BetterTouchTool is in `BetterTouchTool trigger.json`, you will
need to update the path to this repo in the script. Then just add a
BetterTouchTool button to your Stream Deck with the ID `Liked`.
