apitest — standalone API chat tester (NOT connected to the bot code)
====================================================================
1. double-click apitest.bat          -> creates apitest.config.txt on first run
2. open apitest.config.txt, paste API_KEY (and BASE_URL / MODEL if not Featherless)
3. apitest.bat ping                  -> one raw request: shows HTTP status + JSON  (API works?)
4. apitest.bat                       -> chat: you type as the stranger, "eva>" is the AI
   /new  /system  /prompt  /history  /quit
5. edit persona.txt to change how she talks, then /new

Each reply line shows: seconds taken, tokens, persona, and a WARN if the
output would fail the bot's rules (too long, @/link, <think> reasoning).
Needs Go (install.bat of the main project installs it). Builds apitest.exe here.
