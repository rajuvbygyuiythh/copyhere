// apitest — console lab for the API Chat brain (interactive chat, ping,
// scripted scenarios, model compare). Same director / persona / deck code
// the bot uses (internal/bot/apichat); nothing is duplicated.
//
//	apitest.exe            interactive chat (you are the stranger)
//	apitest.exe ping       one raw request, prints status + body
//	apitest.exe scenario <name|all>
//	apitest.exe compare <scenario>
//
// Build from the project root:  go build -o cmd\apitest\apitest.exe .\cmd\apitest
// (or double-click cmd\apitest\apitest.bat).
package main

import "chitchatbot/internal/bot/apichat"

func main() { apichat.LabMain() }
