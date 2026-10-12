EVAChat Flow data pack

input/<category>.txt contains one user-message trigger per line.
output/<category>.txt contains one bot reply per line.
The same category name connects a trigger group to a reply pool.

Snap / Telegram username share:
  output/snap_share.txt is sent once per user. Use %username% in a line and
  it is replaced by a username from the dashboard Message > Usernames list,
  the same way as Single/Folder sequence messages. It is sent when the user
  asks for it (input/snap_request.txt), after a few middle-chat replies, or
  when the user is about to leave. If the Usernames list is empty the share
  step is skipped.

Files are editable. Restart the bot after editing so workers reload the pack.
Use neutral, lawful, consent-aware content only. Do not add personal data,
credentials, explicit content, or content intended to pressure someone.
