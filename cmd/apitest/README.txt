apitest — console lab for the bot's API Chat brain (cmd\apitest, same code as the bot)
=====================================================================================
The director / persona / snap decks live in internal\bot\apichat and are used by BOTH
the dashboard "API Chat" mode and this lab, so what you test here is what the bot sends.
1. double-click apitest.bat          -> creates apitest.config.txt on first run
2. open apitest.config.txt, paste API_KEY (and BASE_URL / MODEL if not Featherless)
3. apitest.bat ping                  -> one raw request: shows HTTP status + JSON  (API works?)
4. apitest.bat                       -> chat: you type as the stranger, "eva>" is the AI
   /new  /state  /system  /prompt  /history  /quit
5. edit persona.txt to change how she talks, then /new
6. edit snap_share.txt to change HOW the snap username is handed out
   (one style per line, %username% required, "a | b" = two messages;
   styles rotate like a shuffled deck so no pattern repeats; /share previews)
7. share mix (anti-spam, apitest.config.txt): out of 10 strangers 3 never get
   the snap (3-4 replies, then the bot closes the chat), 7 do. of those, 60%
   get a 👻 hint first (snap_hint.txt, no username) - if the stranger asks /
   says yes the handle follows, if he ignores it the next reply shares anyway.
   40% share directly. NO_SHARE_PERCENT / HINT_PERCENT change the mix.
8. 20-60 snap usernames: put them in snap_usernames.txt (one per line, copy
   snap_usernames.example.txt). one name per chat from a shuffled deck.
   scenario names: hint_bites / hint_ignores / noshare force that share type.
6. apitest.bat scenario horny        -> plays the scripted stranger from scenarios.txt
   apitest.bat scenario all          -> all scenarios (horny, warm, cold, asl, tricky, noeng, money)
   apitest.bat compare warm          -> same scenario on every COMPARE_MODELS model, side-by-side table
   every chat is appended to apitest.log (send that file for review)
7. HANDOFF.md = where we are / what is next (read first after a break). PLAN.md explains the funnel "director": interest score -> HOT/WARM/COLD ->
   snap shared at reply 4 / 6 / 8 (SHARE_* in config), moods, double lines.
   Set SNAP_USERNAME in apitest.config.txt to see the real share line.

Each reply line shows: seconds taken, tokens, persona, and a WARN if the
output would fail the bot's rules (too long, @/link, <think> reasoning).
Needs Go (install.bat of the main project installs it). apitest.bat builds apitest.exe here
from the project root module (go build .\cmd\apitest).
