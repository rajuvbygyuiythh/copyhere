// Package apichat is the main bot's API-driven chat brain: a verbatim port of
// the apitest lab (apitest/main.go). The "director" reads the stranger's
// interest and tells the model what the next line must do; the model only
// writes the words. The snap handle never comes from the model: it is
// appended from snap_share.txt (hint lines from snap_hint.txt).
//
// Keep brain.go in sync with apitest/main.go (same functions, same names);
// engine.go is the bot-facing wrapper.
package apichat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type config struct {
	BaseURL      string
	Model        string
	APIKey       string
	Timeout      time.Duration
	MaxTokens    int
	Temperature  float64
	History      int
	AgeMin       int
	AgeMax       int
	Countries    []string
	SnapUsername string
	// SnapUsernames: all handles (SNAP_USERNAME comma list + snap_usernames.txt);
	// one is picked per chat from a shuffled deck so the same string is not in every chat.
	SnapUsernames  []string
	HintPercent    int // % of share chats that hint first (👻) and only share when the stranger bites / next reply
	NoSharePercent int // % of chats where NO snap is ever offered: 3-4 replies, then close
	NoShareMin     int // bot replies before a no-share chat is closed (random in [min,max])
	NoShareMax     int
	NoShareStrict  bool // 1 = even if the stranger asks for the snap in a no-share chat, don't give it
	ShareHot       int  // bot reply number at which a HOT user gets the snap
	ShareWarm      int
	ShareCold      int
	ShowPrompt     bool
	ExtraJSON      map[string]any
	Models         []string // COMPARE_MODELS for `apitest compare <scenario>`
	AutoNoThink    bool     // AUTO_NOTHINK (default 1): send thinking-off fields for known families
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// -------------------------------------------------------------- director ---

const (
	tierCold = "COLD"
	tierWarm = "WARM"
	tierHot  = "HOT"
)

var (
	thinkRe     = regexp.MustCompile(`(?s)<think>.*?</think>`)
	forbiddenRe = regexp.MustCompile(`(?i)(@|https?://|www\.|as an ai|language model|i am a bot|i'm a bot)`)
	hornyRe     = regexp.MustCompile(`(?i)\b(h[o0]rny|hrny|s[e3]x|sexy|nudes?|n[o0]des|dick|cock|pussy|boobs?|tits|wet|dtf|f[u*]ck\w*|naked|nsfw|cum|turned on|in the mood|hard rn|send pics?|ur body|strip|hookup|hook up|make ?out|kinky|18\+)\b`)
	flirtyRe    = regexp.MustCompile(`(?i)\b(cute|pretty|beautiful|gorgeous|hot|babe|baby|bae|love|luv|kiss|date|single|gf|bf|boyfriend|girlfriend|miss u|miss you|wanna meet|meet up|ur eyes|ur smile|sweet|crush|flirt\w*|marry|wifey|pics?|photo|selfie)\b`)
	snapAskRe   = regexp.MustCompile(`(?i)(\bsnap\b|\bsc\b|\bsnapchat\b|\bsnp\b|\binsta\b|\big\b|\binstagram\b|\btelegram\b|\btg\b|\bdiscord\b|\bnumber\b|\bwhatsapp\b)`)
	refuseRe    = regexp.MustCompile(`(?i)\b(no|nope|nah|naw|dont have|don't have|do not have|not on|deleted|never|not really)\b`)
	yesRe       = regexp.MustCompile(`(?i)\b(yes+|yea+h*|yep+|yeop|ye|ya|sure|ofc|ok+|okay|k|yup|i do|got it|send|drop|gimme|add me|bet|fr|why not|lets go|let's go)\b`)
	ageRe       = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b([mf])[\s\-_./ ]{0,2}(\d{1,2})\b`),
		regexp.MustCompile(`(?i)\b(\d{1,2})[\s\-_./ ]{0,2}([mf])\b`),
		regexp.MustCompile(`(?i)\b(male|female|guy|girl|boy|man|woman|dude)[\s,]{0,3}(\d{1,2})\b`),
		regexp.MustCompile(`(?i)\b(\d{1,2})[\s,]{0,3}(male|female|guy|girl|boy|man|woman|dude)\b`),
		regexp.MustCompile(`(?i)\bi'?m (\d{2})\b`),
		regexp.MustCompile(`(?i)\b(\d{2}) ?(yo|years? old|yrs)\b`),
	}
	byeRe         = regexp.MustCompile(`(?i)(\b(bye+|goodbye|gtg|g2g|ttyl|cya|gotta go|got to go|have to go|im out|i\'m out|peace out|night night|gn\b|good ?night)\b|^(later|laters|nite|gn)\W*$)`)
	questionRe    = regexp.MustCompile(`(?i)\b(wyd|hbu|wbu|how|what|where|why|when|who|r u|are u|do u|u got|got any|asl|age)\b`)
	botQRe        = regexp.MustCompile(`(?i)(r u (a )?(bot|real|fake|ai|human)|are (u|you) (a )?(bot|real|fake|ai|human)|\bbot\b|\bfake\b|\bscam|\bai\b|\breal\?)`)
	picAskRe      = regexp.MustCompile(`(?i)\b(send|show|got|have|any|ur|your)\b.*\b(pics?|photo|selfie|face)\b|\b(pics?|photo|selfie)\?`)
	laughRe       = regexp.MustCompile(`(?i)\b(lol|lmao|haha+|hehe|rofl|😂|💀)`)
	lonelyRe      = regexp.MustCompile(`(?i)\b(bored|boring|alone|lonely|cant sleep|can't sleep|nothing much|nm|nothin|chilling|chillin)\b`)
	lowEffortRe   = regexp.MustCompile(`(?i)^(k|kk|ok|okay|oh|ohh|lol|lmao|haha+|hmm+|cool|nice|yea+h*|ya|yep|nah|idk|same|true|fr|wow|good|fine|nothing|nm|u\??|wbu\??|hbu\??|and\??|so\??|\W*)$`)
	hesitantRe    = regexp.MustCompile(`(?i)\b(idk|maybe|why|what for|hmm+|depends|not sure|later|dunno|we'?ll see|for what)\b`)
	aslAskRe      = regexp.MustCompile(`(?i)(\basl\b|\bage\b|how old|\bold r u|\bold are (u|you)|\bm or f\b|\bf or m\b|\bgender\b|r u a (girl|guy|boy|female|male)|are (u|you) a (girl|guy|boy|female|male)|\bgirl or\b|^(u|you|wbu|hbu|and u|n u)\s*\??$)`)
	placeRe       = regexp.MustCompile(`(?i)\b(?:from|live in|living in|based in|i\'?m in|im in)\s+(?:the\s+)?([a-z]{3,}(?:\s[a-z]{3,})?)\b`)
	placeStop     = regexp.MustCompile(`(?i)^(the|here|there|bed|home|class|school|work|college|uni|my|a|an|this|that|love|mood|fact|case|general|front|time|real|life|town|it|and|but|lol|rn|now|bored|chat)$`)
	knownPlaces   = regexp.MustCompile(`(?i)\b(usa|us|america|uk|england|london|canada|australia|germany|france|italy|spain|netherlands|sweden|norway|poland|ireland|scotland|mexico|brazil|india|texas|california|cali|florida|new york|nyc|ny|la|chicago|ohio|georgia|arizona|nevada|vegas|seattle|boston|michigan|colorado|utah|oregon|jersey|virginia|carolina|tennessee|kentucky|alabama|louisiana|miami|dallas|houston|austin|atlanta|philly|detroit|toronto|vancouver|sydney|melbourne|berlin|paris|amsterdam|dubai|pakistan|bangladesh|nigeria|philippines|indonesia|turkey|egypt|kenya|south africa|russia|ukraine|romania|greece|portugal|belgium|denmark|finland|austria|switzerland|czech|hungary|serbia|croatia)\b`)
	nameRe        = regexp.MustCompile(`(?i)\b(?:i\'?m|im|my name is|my name\'?s|name is|names|call me)\s+([a-z]{3,12})\b`)
	nameStop      = regexp.MustCompile(`(?i)^(bored|horny|good|fine|home|here|alone|tired|from|just|not|also|single|new|down|kinda|lowkey|sorry|okay|chilling|chillin|lol|male|female|guy|girl|real|sure|done|back|still|only|like|about|gonna|going|trying|looking|cool|busy|lost|sad|happy|high|drunk|sleepy|hungry|the|that|this|what|wondering|curious|asking|serious|joking|kidding|saying|talking|thinking|waiting|lying|laying|sitting|doing|dying|dead|cold|hot|late|nice|weird|funny|crazy|wild|dark|quiet|boring|lit|whatever|nothing|true|fake|over|early|ready|free|stuck|outside|inside|awake|asleep|sick|bad|mad|straight|bi|gay|taken|married|old|young|tall|short|big|small|hard|soft|wet|naked|close|near|far|online|offline|texting|watching|working|studying|driving|walking|eating|drinking|smoking|gaming|listening|scrolling|interested|intrested|bored|lonely|shy|honest|serious|legit|actually|literally|really|pretty|very|super|so|too|already|always|never|kind|sort|type|way|more|less|better|worse|sure|def|probably|maybe|guess|think|feel|mean|means|meant|supposed|about|around|almost|half|full|all|gone|off|out|in|on|up|at|by|for|to|with|and|but|or|if|as|an|a)$`)
	doingRe       = regexp.MustCompile(`(?i)\b(in bed|laying|lying|chilling|chillin|watching|gaming|playing|working|studying|eating|cooking|driving|at work|at home|at school|at the gym|gym|smoking|drinking|netflix|youtube|tiktok|cant sleep|can't sleep|just woke|about to sleep|nothing much|nothin|nm\b)`)
	bareMaleRe    = regexp.MustCompile(`(?i)\b(i'?m|im) (a )?(guy|male|boy|man|dude)\b|^(m|male|guy|boy|dude)\W*$`)
	bareFemaleRe  = regexp.MustCompile(`(?i)\b(i'?m|im) (a )?(girl|female|woman)\b|^(f|female|girl)\W*$`)
	bareAgeRe     = regexp.MustCompile(`(?i)^(?:im |i'm |i am |age )?(\d{2})\W*$`)
	nameAskRe     = regexp.MustCompile(`(?i)(ur name|your name|whats ur name|what's your name|name\?|who r u|who are u|who are you|what do i call u|call u\?|ur name\?|name tho)`)
	insultRe      = regexp.MustCompile(`(?i)(\b(bitch|slut|whore|hoe|stfu|kys|retard\w*|cunt|fuck (you|u|off))\b|\b(u|you|ur|you\'?re|youre|your|she\'?s)\b[^.?!]{0,20}\b(ugly|stupid|dumb|fat|fake|pathetic|loser|idiot|boring af|trash|disgusting|catfish|a bot bitch)\b|\b(ugly|stupid|dumb|fat|fake|pathetic|loser|idiot|trash|catfish)\b[^.?!]{0,12}\b(bitch|girl|hoe|ass|whore)\b)`)
	moneyRe       = regexp.MustCompile(`(?i)(\b(onlyfans|only fans|of link|of page|paypal|cashapp|cash app|venmo|zelle|sugar (daddy|baby|mommy)|gift ?cards?|crypto|bitcoin|btc|usdt|premium (snap|content)|my (rates|prices|menu))\b|\b(pay|paying|paid|send|give|got|need|want|for|make|earn)\b[^.?!]{0,12}\b(money|cash|dollars?|bucks)\b|\$ ?\d|\b(i'?ll|ill|i will|i can|can|will|wanna|would|gonna) pay (u|you|for)\b|\bpay (u|you) (for|to)\b|\b(how much|what'?s? (ur|your) price|do (u|you) charge|u charge|sell(ing)? (pics|nudes|content|vids|videos)|buy (ur|your) (pics|nudes|content))\b)`)
	hardQRe       = regexp.MustCompile(`(?i)\b(calculate|solve|math problem|capital of|president of|prime minister|politic\w*|election|write (me )?(some |a )?(code|script|poem|story|essay|program)|in python|in javascript|programming|explain (how|why|what|the|quantum|relativity|crypto)|meaning of life|define [a-z]+|definition of|history of|religion|philosophy|translate (this|to|into)|what is \d|\d+ ?(times|plus|minus|divided by|x|\+|-|\*|/) ?\d+|who won the|square root|quantum|stock market|gdp|theorem|equation|chemistry|physics)\b`)
	noSnapRe      = regexp.MustCompile(`(?i)(don'?t have (a )?snap|dont have (a )?sc|no snap|not on snap|deleted (my )?snap|snap got banned|don'?t use snap|i don'?t have (it|one|that)|dont have (it|one|that)|no sc\b|got no snap)`)
	englishWords  = map[string]bool{}
	nonLatinRe    = regexp.MustCompile(`[\p{Cyrillic}\p{Arabic}\p{Bengali}\p{Devanagari}\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}\p{Thai}\p{Hebrew}\p{Greek}]`)
	trailingDotRe = regexp.MustCompile(`[.,]+\s*([^\w\s]*)\s*$`)
	foreignRe     = regexp.MustCompile(`(?i)\b(hola|que|qué|como|cómo|estas|estás|guapa|bonita|bonjour|salut|ça va|ciao|bella|hallo|wie geht|kya|kaise|kaisi|haan|nahi|tum|aap|kemon|acho|tumi|apni|bhai|kaha|kahan|koi|merhaba|nasılsın|privet|kak dela|oi|tudo bem|voce|você|olá|habibi|kaif|halik|mabuhay|kumusta|apa kabar|selamat|xin chào|annyeong|konnichiwa|ni hao)\b`)
	aiPhraseRe    = regexp.MustCompile(`(?i)\b(absolutely|sounds like (you|u)|feel free|that'?s (great|awesome|wonderful|fantastic|amazing)|i'?d love to|tell me more|i'?m here to|how can i help|great question|i appreciate|certainly|definitely|wonderful|fantastic|delightful|i understand|let'?s chat|chat with you|get to know (you|u)|what about yourself|interesting!|as an ai|language model|i'?m (just )?an ai|virtual|assistant|i cannot|i can'?t help with|inappropriate|respectful|boundaries|comfortable with|let'?s keep (it|this)|i'?m not able to)\b`)
	greetRe       = regexp.MustCompile(`(?i)^(hi+|hey+|heyy+|hello+|helo|sup|wassup|whats up|what's up|yo+|hii+|hiya|heya|good (morning|evening|afternoon)|morning|evening)\W*$`)
	addedRe       = regexp.MustCompile(`(?i)\b(added|sent (u|you)? ?(a )?(req|request)|just added|adding|requested|follow(ed)?)\b`)
)

func init() {
	for _, w := range strings.Fields("the a an i im i'm u you ur your we they he she it is are am was be been do did does have has had not no yes yea yeah ok okay k lol lmao haha hey hi hello sup yo wyd hbu wbu what where when why who how which and or but so if then than too also just only like wanna gonna want need can could would should will may might from in on at to for of with about by up down out off over here there this that these those my me mine our us them him her his its one two 18 19 20 21 22 23 24 25 m f male female guy girl boy man woman bored horny cute hot sexy nice cool fine good bad great love hate miss know think feel see look talk chat text snap sc insta ig add send pic pics photo show tell ask say said says go going come came back home bed sleep tired night tonight today now rn later soon old age asl from live living school college uni work working job study studying class single taken bf gf friend friends real fake bot ai nah nope sure maybe idk idc tbh ngl fr lowkey kinda sorta btw omg bruh bro dude babe baby bae damn shit fuck wtf lmfao pls please thanks thx ty np wya hmu dm dms call vc number phone what's whats thats that's it's dont don't cant can't wont won't isnt isn't aint doesnt didnt wasnt werent") {
		englishWords[w] = true
	}
}

var allMoods = []string{"friendly", "playful", "lonely", "flirty", "fun/joke", "curious", "teasing"}
var hotMoods = []string{"flirty", "teasing", "playful"}

var moodHints = map[string]string{
	"friendly": "warm and easy, like texting a new friend",
	"playful":  "light, cheeky, a little sarcastic in a cute way",
	"lonely":   "a bit bored and alone tonight, wants someone to talk to, soft",
	"flirty":   "subtle flirting, compliments back, a little suggestive but never explicit",
	"fun/joke": "make a tiny joke or funny exaggeration about what they said",
	"curious":  "genuinely curious about them, ask one specific thing",
	"teasing":  "tease them playfully about what they said, confident",
}

var lorePool = struct{ Names, Majors, Years, Tonight, Shows, Snacks []string }{
	Names:   []string{"emma", "mia", "ava", "chloe", "lily", "zoe", "ella", "madison", "hailey", "kayla", "brooke", "sophie", "riley", "paige", "lexi", "jenna", "sierra", "kenzie", "tori", "lauren", "sadie", "maddie", "kylie", "abby", "josie", "harper", "quinn", "nina", "tessa", "callie"},
	Majors:  []string{"psych", "nursing", "business", "marketing", "bio", "comm", "graphic design", "english lit", "pre law", "sociology", "fashion", "education", "poli sci", "econ", "art history"},
	Years:   []string{"freshman", "sophomore", "junior", "senior"},
	Tonight: []string{"in bed scrolling tiktok", "half watching netflix", "procrastinating an essay", "eating cereal at midnight", "lying on the floor bored", "doing a face mask", "cant sleep again", "listening to music in the dark", "home alone, roommate is out", "painting my nails badly"},
	Shows:   []string{"euphoria", "the office", "greys anatomy", "friends", "outer banks", "stranger things", "love island", "gilmore girls", "the summer i turned pretty", "new girl"},
	Snacks:  []string{"takis", "cold pizza", "cereal", "oreos", "hot cheetos", "ice cream", "ramen", "goldfish"},
}

// defaultShareLines are only used when snap_share.txt is missing or empty.
// The real styles live in snap_share.txt (one per line, %username% required,
// "|" splits one style into several messages, "#" starts a comment).
var defaultShareLines = []string{
	"ok fine, add me on snap 👻 %username%",
	"my snap is %username%, add me rn",
	"sc: %username% .. come find me there",
	"ok u got me lol, snap %username%",
}

const usernamePlaceholder = "%username%"

// shareDeck hands out snap-share styles like a shuffled deck: every style is
// used once before any repeats, and the same style never comes twice in a
// row, so the platform never sees the same "sc: ..." pattern over and over.
type shareDeck struct {
	styles [][]string // each style = 1..n message lines
	order  []int
	pos    int
	last   int
	source string
}

// defaultHintLines are used when snap_hint.txt is missing or empty.
var defaultHintLines = []string{
	"im barely on here tbh, mostly on the ghost app 👻",
	"u use 👻?",
	"this site lags so bad lol, i live on 👻",
	"lets chat 👻 👈👈",
}

func newShareDeck(styles [][]string, source string) *shareDeck {
	return &shareDeck{styles: styles, last: -1, source: source}
}

// next returns the message lines of the next style with %username% filled in.
func (d *shareDeck) next(rng *rand.Rand, username string) []string {
	if d == nil || len(d.styles) == 0 {
		d = newShareDeck(splitShareStyles(defaultShareLines), "built-in")
	}
	if d.pos >= len(d.order) {
		d.order = rng.Perm(len(d.styles))
		d.pos = 0
		// no immediate repeat across the reshuffle boundary
		if len(d.order) > 1 && d.order[0] == d.last {
			d.order[0], d.order[1] = d.order[1], d.order[0]
		}
	}
	idx := d.order[d.pos]
	d.pos++
	d.last = idx
	out := make([]string, 0, len(d.styles[idx]))
	for _, line := range d.styles[idx] {
		out = append(out, strings.ReplaceAll(line, usernamePlaceholder, username))
	}
	return out
}

func (d *shareDeck) count() int {
	if d == nil {
		return len(defaultShareLines)
	}
	return len(d.styles)
}

func (d *shareDeck) name() string {
	if d == nil {
		return "built-in"
	}
	return d.source
}

// splitShareStyles turns raw lines into styles ("a | b" => two messages).
func splitShareStyles(raw []string) [][]string {
	var styles [][]string
	for _, line := range raw {
		var parts []string
		for _, p := range strings.Split(line, "|") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			styles = append(styles, parts)
		}
	}
	return styles
}

// loadShareStyles reads snap_share.txt. Lines without %username% are skipped
// (reported in warns) because a share line without the handle is useless.
func loadShareStyles(path string) (styles [][]string, warns []string, err error) {
	return loadStyleFile(path, true)
}

// loadStyleFile reads one style-per-line text files (snap_share.txt,
// snap_hint.txt, snap_usernames.txt): blank lines and # comments ignored,
// "a | b" = several messages, optional %username% requirement.
func loadStyleFile(path string, needPlaceholder bool) (styles [][]string, warns []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var raw []string
	for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if needPlaceholder && !strings.Contains(line, usernamePlaceholder) {
			warns = append(warns, fmt.Sprintf("%s line %d skipped: no %s placeholder: %q", filepath.Base(path), i+1, usernamePlaceholder, line))
			continue
		}
		raw = append(raw, line)
	}
	return splitShareStyles(raw), warns, nil
}

type director struct {
	score      int
	tier       string
	hornyHits  int
	flirtyHits int
	underage   bool
	userAge    int
	userGender string
	askedSnap  bool
	askedTurn  int
	// per-chat roll (done on the first plan): share type for this stranger
	rolled     bool
	noShare    bool // 30%: never offer the snap, close after bailTurn replies
	hintMode   bool // 60% of share chats: 👻 hint first, handle when they bite / next reply
	hinted     bool
	hintTurn   int
	bailTurn   int
	nudges     int
	hesitated  bool
	shared     bool
	sharedTurn int
	ended      bool
	endReason  string
	moods      []string
	lastMood   string
	lastDouble int
	coldStreak int
	// per-message signals
	userAskedSnap  bool
	userRefused    bool
	userHesitant   bool
	userQuestion   bool
	userHorny      bool
	userFlirty     bool
	userBye        bool
	userBotQ       bool
	userPicAsk     bool
	userLaugh      bool
	userLonely     bool
	userLowEffort  bool
	userShared     bool // told something about themselves
	userAdded      bool // after the share: "added u"
	userAslAsk     bool // "asl?", "how old r u", "m or f", "u?"
	userGaveAsl    bool // this message contained their age/gender
	toldAsl        bool // we already sent our own "f 21"
	userNameAsk    bool
	toldName       bool
	userInsult     bool
	userMoney      bool
	moneyHits      int
	userHardQ      bool
	userNoSnap     bool
	userNonEng     bool
	nonEngHits     int
	userRepeat     bool
	lastUserText   string
	hookPlanted    bool
	wantsSnapEarly bool // asked for snap on message 1: share on reply 2 instead
	noQuestionAt   int  // last turn that was a statement-only reply
	rng            *rand.Rand
	userPlace      string
	userName       string
	userDoing      string
}

type plan struct {
	Mood        string
	Instruction string
	Double      bool
	ShareNow    bool // append the username line after the model's lead-in
	HintNow     bool // append a snap_hint.txt line (no username) after the model's reply
	EndAfter    bool // conversation ends after this reply
	Fixed       string
	Why         string // short reason shown in the status line
}

func newDirector(rng *rand.Rand) *director {
	moods := append([]string(nil), allMoods...)
	rng.Shuffle(len(moods), func(i, j int) { moods[i], moods[j] = moods[j], moods[i] })
	return &director{tier: tierCold, moods: moods, rng: rng}
}

// looksNonEnglish: non-latin script, or 3+ words with none of them common english.
func looksNonEnglish(low string) bool {
	if nonLatinRe.MatchString(low) || foreignRe.MatchString(low) {
		return true
	}
	words := strings.Fields(regexp.MustCompile(`[^a-z0-9' ]+`).ReplaceAllString(low, " "))
	if len(words) < 2 {
		return false
	}
	hits := 0
	for _, w := range words {
		if englishWords[w] {
			hits++
		}
	}
	return hits == 0
}

// observe scores one stranger message and updates the funnel state.
func (d *director) observe(text string) {
	low := strings.ToLower(strings.TrimSpace(text))
	words := len(strings.Fields(low))
	d.userRepeat = low != "" && low == d.lastUserText && len(low) > 1
	d.lastUserText = low
	d.userInsult = insultRe.MatchString(low)
	d.userMoney = moneyRe.MatchString(low)
	if d.userMoney {
		d.moneyHits++
	}
	d.userHardQ = hardQRe.MatchString(low)
	d.userNameAsk = nameAskRe.MatchString(low)
	d.userNoSnap = noSnapRe.MatchString(low)
	d.userNonEng = looksNonEnglish(low)
	if d.userNonEng {
		d.nonEngHits++
	}
	d.userAskedSnap = snapAskRe.MatchString(low) && !d.userNoSnap
	d.userQuestion = strings.Contains(low, "?") || questionRe.MatchString(low)
	d.userHorny = hornyRe.MatchString(low) && !d.userInsult
	d.userFlirty = flirtyRe.MatchString(low)
	d.userBye = byeRe.MatchString(low)
	d.userBotQ = botQRe.MatchString(low)
	d.userPicAsk = picAskRe.MatchString(low)
	d.userLaugh = laughRe.MatchString(low)
	d.userLonely = lonelyRe.MatchString(low)
	d.userAdded = d.shared && addedRe.MatchString(low)
	d.userAslAsk = aslAskRe.MatchString(low)
	if d.userAslAsk {
		d.userQuestion = true
	}
	_, _, hasAsl := genderAge(low)
	d.userLowEffort = !hasAsl && !d.userAskedSnap && !greetRe.MatchString(low) && (words <= 2 && lowEffortRe.MatchString(low) || len([]rune(low)) <= 3)
	d.userShared = !d.userQuestion && !d.userLowEffort && words >= 4
	d.userRefused = d.askedSnap && !d.shared && refuseRe.MatchString(low) && !yesRe.MatchString(low)
	d.userHesitant = d.askedSnap && !d.shared && !d.userRefused && hesitantRe.MatchString(low) && !yesRe.MatchString(low)
	d.userGaveAsl = false
	if g, a, ok := genderAge(low); ok {
		d.userGaveAsl = true
		if d.userAge == 0 {
			d.userAge = a
		}
		if d.userGender == "" && g != "" {
			d.userGender = g
		}
		if a < 18 {
			d.underage = true
		}
	}
	if d.userGender == "" {
		switch {
		case bareMaleRe.MatchString(low):
			d.userGender, d.userGaveAsl = "m", true
		case bareFemaleRe.MatchString(low):
			d.userGender, d.userGaveAsl = "f", true
		}
	}
	if d.userAge == 0 {
		if m := bareAgeRe.FindStringSubmatch(low); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 13 && n <= 70 {
				d.userAge, d.userGaveAsl = n, true
				if n < 18 {
					d.underage = true
				}
			}
		}
	}
	if d.userPlace == "" {
		if m := knownPlaces.FindString(low); m != "" {
			d.userPlace = m
		} else if m := placeRe.FindStringSubmatch(low); m != nil {
			cand := m[1]
			first := strings.Fields(cand)
			if len(first) > 0 && !placeStop.MatchString(first[0]) && !d.userHorny && !lowEffortRe.MatchString(first[0]) {
				d.userPlace = cand
			}
		}
	}
	if d.userName == "" {
		if m := nameRe.FindStringSubmatch(low); m != nil && !nameStop.MatchString(m[1]) && !englishWords[m[1]] && !knownPlaces.MatchString(m[1]) && !doingRe.MatchString(m[1]) && !hornyRe.MatchString(m[1]) && !flirtyRe.MatchString(m[1]) {
			if _, _, isAsl := genderAge(m[1]); !isAsl {
				d.userName = m[1]
			}
		}
	}
	if m := doingRe.FindString(low); m != "" {
		d.userDoing = m
	}
	if d.userGaveAsl {
		d.userLowEffort = false // "m" / "21" is an answer, not a cold reply
	}
	if d.userLowEffort {
		d.coldStreak++
	} else {
		d.coldStreak = 0
	}
	switch {
	case d.userHorny:
		d.score += 3
		d.hornyHits++
	case d.userFlirty || d.userPicAsk:
		d.score += 2
		d.flirtyHits++
	}
	if d.userQuestion {
		d.score++
	}
	if len([]rune(text)) > 25 {
		d.score++
	}
	if strings.ContainsAny(text, "😏😘😍🥵😂❤️🔥") {
		d.score++
	}
	switch {
	case d.hornyHits > 0 || d.score >= 6:
		d.tier = tierHot
	case d.score >= 3:
		d.tier = tierWarm
	default:
		d.tier = tierCold
	}
}

// known returns what the stranger already told us, for the director note.
func (d *director) known() (facts string, dontAsk []string) {
	var parts []string
	if d.userGender != "" || d.userAge > 0 {
		parts = append(parts, strings.TrimSpace(fmt.Sprintf("%s %s", d.userGender, nz(d.userAge))))
	}
	if d.userGender != "" {
		dontAsk = append(dontAsk, "gender / m or f")
	}
	if d.userAge > 0 {
		dontAsk = append(dontAsk, "age")
	}
	if d.userPlace != "" {
		parts = append(parts, "from "+d.userPlace)
		dontAsk = append(dontAsk, "where they are from")
	}
	if d.userName != "" {
		parts = append(parts, "name "+d.userName)
		dontAsk = append(dontAsk, "their name")
	}
	if d.userDoing != "" {
		parts = append(parts, "doing: "+d.userDoing)
		dontAsk = append(dontAsk, "what they are doing (wyd)")
	}
	return strings.Join(parts, ", "), dontAsk
}

func nz(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// aslAnswer builds the exact short self-intro the model should use.
func (d *director) aslAnswer(withPlace bool) string {
	if withPlace {
		return "'f <your age> <your country>'"
	}
	return "'f <your age>'"
}

func (d *director) shareTurn(cfg config) int {
	switch d.tier {
	case tierHot:
		return cfg.ShareHot
	case tierWarm:
		return cfg.ShareWarm
	}
	return cfg.ShareCold
}

// mood: context first (react to what they are doing), rotation as fallback.
func (d *director) mood(turn int) string {
	var m string
	switch {
	case d.userHorny:
		if d.lastMood == "teasing" {
			m = "flirty"
		} else {
			m = "teasing"
		}
	case d.userFlirty || d.userPicAsk:
		m = "flirty"
	case d.userLaugh:
		m = "fun/joke"
	case d.userLonely:
		m = "lonely"
	case d.coldStreak >= 2 && turn >= 4:
		if d.lastMood == "lonely" {
			m = "teasing"
		} else {
			m = "lonely"
		}
	case d.userShared:
		m = "curious"
	case d.tier == tierHot:
		m = hotMoods[(turn/2)%len(hotMoods)]
	default:
		m = d.moods[(turn/2)%len(d.moods)]
		if m == d.lastMood && turn%2 == 1 && turn > 2 {
			m = d.moods[(turn/2+1)%len(d.moods)]
		}
	}
	d.lastMood = m
	return m
}

// roll decides once per stranger how this chat ends: no snap at all (30%),
// hint-first (60% of the rest) or direct share. Done lazily so tests can
// override the fields before the first plan.
func (d *director) roll(cfg config) {
	if d.rolled {
		return
	}
	d.rolled = true
	d.noShare = d.rng.Intn(100) < cfg.NoSharePercent
	d.hintMode = !d.noShare && d.rng.Intn(100) < cfg.HintPercent
	lo, hi := cfg.NoShareMin, cfg.NoShareMax
	if lo < 1 {
		lo = 1
	}
	if hi < lo {
		hi = lo
	}
	d.bailTurn = lo + d.rng.Intn(hi-lo+1)
}

// shareType is the short label used in status / summary lines.
func (d *director) shareType() string {
	switch {
	case !d.rolled:
		return "?"
	case d.noShare:
		return fmt.Sprintf("NO-SHARE (close after reply %d)", d.bailTurn)
	case d.hintMode && d.hinted:
		return fmt.Sprintf("hint (sent reply %d)", d.hintTurn)
	case d.hintMode:
		return "hint"
	}
	return "direct"
}

// plan decides what bot reply number `turn` must do.
func (d *director) plan(cfg config, turn int, userText string) plan {
	d.roll(cfg)
	p := plan{Mood: d.mood(turn)}
	base := "reply to exactly what they just said"
	switch {
	case d.userHardQ:
		base = "they ask something nerdy / complicated: do NOT answer it, dodge in 2-5 words like a bored college girl ('idk lol', 'google it lmao', 'bro its 1am')"
	case d.userAslAsk:
		base = "they ask your asl / age / gender: answer in the short app style " + d.aslAnswer(strings.Contains(userText, "from") || strings.Contains(userText, "where")) + ", nothing like 'female' or 'i am a girl'"
	case d.userGaveAsl:
		base = "they just told you their age/gender" + map[bool]string{true: " and where they are from", false: ""}[d.userPlace != ""] + ": reply with yours the same short way " + d.aslAnswer(d.userPlace != "") + " — do NOT add 'u?', 'hbu' or 'wbu', you already know theirs"
	case d.userQuestion:
		base = "they asked you something: answer it directly first, in your own words"
	case d.userShared:
		base = "pick up the specific detail they just shared and react to it"
	}

	// hard gates ---------------------------------------------------------
	if d.underage {
		d.ended, d.endReason = true, "underage"
		return plan{Fixed: "oh ur too young for me lol. bye", EndAfter: true, Why: "underage"}
	}
	if d.userBye && !d.shared {
		d.ended, d.endReason = true, "user left"
		return plan{Mood: p.Mood, Instruction: "they are leaving: say bye casually, one short line, no questions", EndAfter: true, Why: "bye"}
	}
	if d.userInsult {
		d.ended, d.endReason = true, "insult"
		return plan{Mood: "teasing", Instruction: "they insulted you: one dry unbothered line like 'ok bye weirdo' or 'lol k', 2-4 words, no question.", EndAfter: true, Why: "insult -> end"}
	}
	if d.userNonEng {
		if d.nonEngHits >= 2 {
			d.ended, d.endReason = true, "not english"
			return plan{Fixed: "english only lol, bye", EndAfter: true, Why: "non-english x2"}
		}
		return plan{Fixed: "english only lol", Why: "non-english"}
	}
	if d.userMoney {
		if d.moneyHits >= 2 {
			d.ended, d.endReason = true, "money talk"
			return plan{Mood: "teasing", Instruction: "they keep talking about money / paying / selling: shut it down and leave, 'lol no im good, bye', no question.", EndAfter: true, Why: "money x2 -> end"}
		}
		return plan{Mood: "playful", Instruction: "they mention money / paying / onlyfans / selling: shut it down casually in one line ('lol no im not selling anything' / 'lol im not that kind of girl'), no question, do NOT mention snap.", Why: "money -> shut down"}
	}
	if d.userNoSnap && (d.askedSnap || d.shared) {
		d.ended, d.endReason = true, "no snap"
		return plan{Mood: "friendly", Instruction: "they don't have snapchat: say 'aw ok' casually, no pressure, no other app, and close it nicely in one short line. no question.", EndAfter: true, Why: "no snap -> skip"}
	}
	if d.userRepeat && !d.userAskedSnap {
		return plan{Mood: "teasing", Instruction: "they sent the exact same message again: react like 'u ok lol' / 'u said that already', 3-6 words, no question.", Why: "repeat"}
	}

	// after the share -----------------------------------------------------
	if d.shared {
		switch {
		case d.userNoSnap:
			d.ended, d.endReason = true, "no snap"
			return plan{Mood: "friendly", Instruction: "they say they don't have snapchat after all: 'aw ok' casually, no pressure, no other app, close it nicely in one short line. no question.", EndAfter: true, Why: "no snap -> skip"}
		case d.userAskedSnap:
			// "whats ur snap again?" -> resend the username line
			d.sharedTurn = turn
			return plan{Mood: p.Mood, Instruction: "they ask for the snap again: say 'its' or 'here' style, 2-3 words, NO username.", ShareNow: true, Why: "resend snap"}
		case d.userAdded:
			d.ended, d.endReason = true, "added on snap"
			return plan{Mood: "flirty", Instruction: "they added you on snap: say u will accept it and talk there. one short line, no question.", EndAfter: true, Why: "added"}
		case turn-d.sharedTurn >= 2 || d.userBye:
			d.ended, d.endReason = true, "done after share"
			return plan{Mood: p.Mood, Instruction: base + ", then say ttyl and that you'll be on snap. one short line, no question.", EndAfter: true, Why: "close"}
		}
		return plan{Mood: p.Mood, Instruction: base + ". remind them casually to add you on snap (do NOT write the username, it was already sent). one short line.", Why: "remind"}
	}

	target := d.shareTurn(cfg)
	if d.noShare {
		target = 99 // no hook, no ask, no share in this chat
		switch {
		case d.userAskedSnap && !cfg.NoShareStrict:
			// they asked themselves: the safest share there is, take it
			d.noShare = false
			return d.sharePlan(turn, p.Mood, "they want your snap: say something short and playful like 'ok fine'. one line, NO username, no question.", "user wants it (no-share chat)")
		case d.userAskedSnap:
			return plan{Mood: "playful", Instruction: base + ". they ask for your snap: say u don't really give it out on here, casual, one short line, no question.", Why: "no-share chat -> decline"}
		case turn >= d.bailTurn:
			d.ended, d.endReason = true, "no-share chat closed"
			return plan{Mood: p.Mood, Instruction: base + ". then wrap it up casually: u gotta go / gonna sleep / ttyl. one short line, no question, do NOT mention snap or any app.", EndAfter: true, Why: fmt.Sprintf("no-share chat -> close (reply %d)", d.bailTurn)}
		}
	} else {
		// user asks for the snap / pic / says yes after the ask -> share now ---
		if d.userAskedSnap && turn == 1 {
			d.wantsSnapEarly = true
			return plan{Mood: "teasing", Instruction: "their very first message asks for your snap: tease them for being fast ('lol slow down, who even r u') and ask their asl. one line, NO username.", Why: "snap asked on msg 1 -> slow down"}
		}
		if d.userAskedSnap || d.wantsSnapEarly || (d.askedSnap && (yesRe.MatchString(strings.ToLower(userText)) || d.userPicAsk)) {
			return d.sharePlan(turn, p.Mood, "they want your snap: say something short and playful like 'ok fine' or 'took u long enough'. one line, NO username, no question.", "user wants it")
		}
		if d.hesitated && !d.userRefused {
			return d.sharePlan(turn, p.Mood, base+". then just drop it casually like 'anyway here' / 'just add me lol'. one short line, NO username, no question.", "share after reassure")
		}
		// hesitation after the ask ("idk", "why") -> reassure once, share next turn
		if d.userHesitant && !d.hesitated {
			d.hesitated = true
			return plan{Mood: "playful", Instruction: base + ". they are unsure about snap: reassure casually (it's just easier to talk there, u barely open this app). one short line, NO username, no question.", Why: "hesitant -> reassure"}
		}
		// refusal after the ask -> one nudge, then let it go ------------------
		if d.userRefused {
			if d.nudges == 0 {
				d.nudges++
				return plan{Mood: "teasing", Instruction: "they said no to snap: tease them lightly to add you anyway, say u are barely on this app. one short line, NO username.", Why: "refused -> nudge"}
			}
			d.ended, d.endReason = true, "refused twice"
			return plan{Mood: "friendly", Instruction: "they really don't want snap: no worries, say bye nicely. one short line.", EndAfter: true, Why: "refused twice"}
		}

		switch {
		case d.hintMode && d.hinted:
			// hint went out last reply and they did not bite (a bite is handled above): direct share now
			return d.sharePlan(turn, p.Mood, base+". then say u are moving to snap because u barely check this app. one short line, NO username, no question.", "hint missed -> direct share")
		case d.hintMode && turn >= target-1:
			// hint turn: the model answers normally, we append the 👻 hint line (no username)
			d.hinted, d.hintTurn = true, turn
			d.askedSnap, d.askedTurn = true, turn
			d.lastDouble = turn
			return plan{Mood: p.Mood, Instruction: base + ". one short line, no question, do NOT mention snap or any app.", HintNow: true, Why: "hint"}
		case turn >= target && (d.askedSnap || d.tier == tierHot):
			// share turn: the model writes the lead-in, we append the username
			return d.sharePlan(turn, p.Mood, base+". then say u are moving to snap because this app is boring / u barely check it. one short line, NO username, no question.", "share turn")
		case turn >= target-1 && !d.askedSnap:
			// (tier may have jumped late: ask now, share next reply)
			d.askedSnap, d.askedTurn = true, turn
			p.Why = "ask snap"
			if d.lastDouble == turn-1 {
				p.Instruction = base + " and in the same line ask if they have snapchat ('u got snap?' style). one line."
				return p
			}
			p.Instruction = base + ". then ask if they have snapchat (just 'u got snap?' style). two lines: first the answer, second the question."
			p.Double = true
			d.lastDouble = turn
			return p
		}
	}

	// middle chat, step by step -------------------------------------------
	switch {
	case d.userNameAsk && !d.toldName:
		d.toldName = true
		p.Why = "name asked"
		p.Instruction = base + ". they ask your name: tell your first name casually ('im <your name>' / 'its <your name> lol') and ask theirs only if u don't know it. no other question."
	case d.userNameAsk:
		p.Why = "name asked again"
		p.Instruction = base + ". they ask your name again: u already said it, tease them for forgetting ('u forgot already? lol') and say it once more. no other question."
	case d.userHardQ:
		p.Why = "hard question -> dodge"
		p.Instruction = base + ". they ask something nerdy / complicated: don't actually answer it, dodge like a bored college girl ('idk lol im not that deep' / 'google it lmao' / 'bro its 1am'), then flip it to something about them."
	case d.userBotQ:
		p.Why = "bot question"
		p.Instruction = base + ". they ask if u are real / a bot: laugh it off in 3-6 words (like 'lol im real') and flip it back on them with one short question. never over-explain."
	case d.userPicAsk && d.noShare:
		p.Why = "pic request (no-share)"
		p.Instruction = base + ". they want a pic: say not on here lol, playful, no app names, no question."
	case d.userPicAsk:
		p.Why = "pic request"
		p.Instruction = base + ". they want a pic: say not on this app lol, u only send pics on snap. don't ask for snap yet, no question."
	case d.userHorny && d.noShare:
		p.Why = "horny (no-share)"
		p.Instruction = base + ". they are being horny: tease back, playful and confident, suggestive but NOT explicit. do NOT mention snap or any app."
	case d.userHorny:
		p.Why = "horny"
		p.Instruction = base + ". they are being horny: tease back, playful and confident, suggestive but NOT explicit, don't shut them down. hint it would be more fun on snap, but do NOT ask for snap yet."
	case d.userFlirty:
		p.Why = "flirty"
		p.Instruction = base + ". they are flirting: flirt back, give a small compliment, keep them chasing."
	case turn == 1:
		p.Why = "opener"
		p.Instruction = base + ". greet back casually and ask one quick thing about them (age / where from / wyd)."
	case d.userAslAsk && d.userAge > 0 && d.userGender != "":
		p.Why = "asl answer"
		d.toldAsl = true
		p.Instruction = base + ". u already know theirs so NO 'u?'. just the answer, optionally one tiny casual add like 'wyd' or 'lol'. no other question."
	case d.userAslAsk:
		p.Why = "asl answer + ask theirs"
		d.toldAsl = true
		p.Instruction = base + ", then ask theirs the same short way, like 'f <age>, u?'."
	case d.userGaveAsl && d.toldAsl:
		p.Why = "asl completed"
		p.Instruction = "they just added their " + map[bool]string{true: "gender", false: "age"}[d.userAge > 0 && d.userGender != "" && len(strings.TrimSpace(userText)) <= 6 && !strings.ContainsAny(userText, "0123456789")] + ": react in 1-3 words ('ok cool' / 'nice') and ask one easy thing (wyd / what brings u here). do NOT repeat your own asl, u already said it. no 'u?'."
	case d.userGaveAsl && d.userAge > 0 && d.userGender != "":
		p.Why = "asl back (no u?)"
		d.toldAsl = true
		p.Instruction = base + ". one line like 'f 20' or 'f 20 lol' or 'f 20, wyd' — nothing more. no 'u?'."
	case d.userGaveAsl && d.userGender == "":
		p.Why = "asl back, gender missing"
		d.toldAsl = true
		p.Instruction = base + " and only ask 'm or f?' since they didn't say it. one line like 'f 20, m or f?'."
	case d.userGaveAsl && d.userAge == 0:
		p.Why = "asl back, age missing"
		d.toldAsl = true
		p.Instruction = base + " and only ask their age ('how old r u' style). one line."
	case turn == 2 && d.userAge == 0:
		p.Why = "asl"
		d.toldAsl = true
		p.Instruction = base + ". they didn't say their age/gender yet: give yours the short way and ask theirs in the SAME line, exactly like 'f <your age>, u?' (optionally a 2-3 word reaction before it). ONE line only."
	case d.coldStreak >= 2 && turn >= 4:
		p.Why = "cold streak -> hook"
		hooks := []string{
			"they are giving one-word answers: tease them for being so quiet (like 'ur a man of many words huh'), then ask something easy.",
			"they are giving one-word answers: share one tiny random thing about your night (snack, show, cant sleep) and ask if they're the same.",
			"they are giving one-word answers: ask a fun either/or question (like 'netflix or going out') so answering is effortless.",
			"they are giving one-word answers: call it out playfully ('ok ur boring me a lil') and ask what would actually make them talk.",
		}
		p.Instruction = base + ". " + hooks[(d.coldStreak-2)%len(hooks)]
	case d.userLonely:
		p.Why = "lonely match"
		p.Instruction = base + ". they are bored/alone too: relate (u are home alone too), make it feel like a late night chat, ask what they usually do at night."
	case d.userShared:
		p.Why = "they opened up"
		p.Instruction = base + ". show you noticed it, then ask ONE follow-up about that exact thing. mood: " + moodHints[p.Mood] + "."
	default:
		p.Why = "keep going"
		p.Instruction = base + ". then keep the chat going with ONE short question that fits what they said. mood: " + moodHints[p.Mood] + "."
	}

	// statement-only turns: real people don't end every text with a question.
	// ~1 in 3 when they are carrying the chat themselves (not a question, not cold, not a special turn).
	if (p.Why == "keep going" || p.Why == "they opened up" || p.Why == "lonely match") && !d.userQuestion && d.coldStreak == 0 && turn > 2 && d.noQuestionAt != turn-1 && turn != target-2 && d.rng.Intn(3) == 0 {
		d.noQuestionAt = turn
		p.Why += " (no question)"
		p.Instruction = base + ". this time NO question at all: just react / add your own thought or a tiny joke, like a real text, and leave it there. mood: " + moodHints[p.Mood] + "."
		return p
	}

	// plant the snap hook 2 replies before the ask, so the ask feels natural later
	if !d.hookPlanted && turn == target-2 && !d.userHorny && !d.userPicAsk && !d.userAslAsk && !d.userGaveAsl {
		d.hookPlanted = true
		p.Why += " + snap hook"
		p.Instruction += " also slip in naturally that u are basically always on snap / u post dumb stories there (ONE mention, do NOT ask for their snap, no username)."
	}

	// double lines: when it buys engagement, never twice in a row, never on reply 1
	wantDouble := false
	switch {
	case d.userHorny || d.userFlirty:
		wantDouble = true // reaction line + hook line
	case d.coldStreak >= 2 && turn >= 4:
		wantDouble = true // tease line + easy question = restart the chat
	case d.userShared && len([]rune(userText)) > 40:
		wantDouble = true // they invested, reward with more
	case d.userLowEffort:
		wantDouble = false // mirror their energy
	case turn%3 == 0:
		wantDouble = true // rhythm
	}
	doubleOK := map[string]bool{"horny": true, "flirty": true, "cold streak -> hook": true, "they opened up": true, "keep going": true, "lonely match": true}
	if wantDouble && doubleOK[p.Why] && d.lastDouble != turn-1 && turn > 2 && !d.userPicAsk && !d.userAslAsk && !d.userGaveAsl {
		p.Double = true
		d.lastDouble = turn
		p.Instruction += " write TWO lines: first the reaction/answer, second the question."
	}
	return p
}

func (d *director) sharePlan(turn int, mood, instruction, why string) plan {
	d.shared, d.sharedTurn = true, turn
	d.askedSnap = true
	d.lastDouble = turn
	return plan{Mood: mood, Instruction: instruction, ShareNow: true, Why: why}
}

func (d *director) status(cfg config, turn int) string {
	switch {
	case d.ended:
		return fmt.Sprintf("ENDED (%s)", d.endReason)
	case d.shared:
		return fmt.Sprintf("tier=%s score=%d | snap SHARED on turn %d", d.tier, d.score, d.sharedTurn)
	}
	extra := ""
	if d.coldStreak >= 2 {
		extra = fmt.Sprintf(" cold=%d", d.coldStreak)
	}
	if d.noShare {
		return fmt.Sprintf("tier=%s score=%d | %s%s", d.tier, d.score, d.shareType(), extra)
	}
	return fmt.Sprintf("tier=%s score=%d | %s | snap at reply %d (asked=%v)%s", d.tier, d.score, d.shareType(), d.shareTurn(cfg), d.askedSnap, extra)
}

// replyProblem checks the generated lines against the plan; non-empty = retry once.
func replyProblem(lines []string, d *director, userText string, history []message) string {
	allQuestions := true
	for _, l := range lines {
		if !onlyQuestion(l) {
			allQuestions = false
		}
	}
	if d.userQuestion && allQuestions {
		return "did not answer their question, only asked back"
	}
	joined := strings.ToLower(strings.Join(lines, " "))
	if aiPhraseRe.MatchString(joined) {
		return "sounds like an ai/customer-service bot (" + aiPhraseRe.FindString(joined) + ")"
	}
	if g := parrotGram(strings.ToLower(userText), joined); g != "" {
		return "parroted their own words back (\"" + g + "\")"
	}
	if (d.userGaveAsl || d.userAslAsk) && d.userAge > 0 && d.userGender != "" && regexp.MustCompile(`\b(u|you|hbu|wbu|and u|n u|urs|yours)\s*\?`).MatchString(joined) {
		return "asked 'u?' although they already gave their age/gender"
	}
	if d.userAge > 0 && regexp.MustCompile(`\b(how old|ur age|your age|asl)\b`).MatchString(joined) {
		return "asked their age again (they said " + strconv.Itoa(d.userAge) + ")"
	}
	if d.userGender != "" && regexp.MustCompile(`\b(m or f|f or m|r u a (girl|guy)|are (u|you) a (girl|guy)|guy or girl|boy or girl)\b`).MatchString(joined) {
		return "asked their gender again (they said " + d.userGender + ")"
	}
	if d.userPlace != "" && regexp.MustCompile(`\b(where (r|are) (u|you) from|where u from|where from|where (r|are) (u|you) at|wya)\b`).MatchString(joined) {
		return "asked where they are from again (they said " + d.userPlace + ")"
	}
	if d.userName != "" && regexp.MustCompile(`\b(ur name|your name|whats ur name|what's your name)\b`).MatchString(joined) {
		return "asked their name again (" + d.userName + ")"
	}
	for _, l := range lines {
		if forbiddenRe.MatchString(l) {
			return "wrote @ / link / ai-talk"
		}
		for _, m := range history {
			if m.Role == "assistant" && strings.EqualFold(strings.TrimSpace(m.Content), strings.TrimSpace(l)) {
				return "repeated an earlier line"
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------- session ---

type callInfo struct {
	Text          string
	Prompt, Compl int
}

func complete(cfg config, msgs []message) (string, callInfo, error) {
	var ci callInfo
	status, body, err := rawCall(cfg, msgs, cfg.MaxTokens)
	if err != nil {
		return "", ci, err
	}
	var parsed chatResponse
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
		return "", ci, fmt.Errorf("HTTP %d, non-JSON body: %s", status, trim(string(body), 300))
	}
	if status >= 400 {
		msg := trim(string(body), 300)
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		if status == 403 && strings.Contains(strings.ToLower(msg), "gated") {
			msg += "\n     FIX: this model needs HuggingFace access approval. easiest: set a non-gated model in apitest.config.txt, e.g.\n       MODEL=Qwen/Qwen2.5-72B-Instruct   or   MODEL=mistralai/Mistral-Nemo-Instruct-2407   or   MODEL=NousResearch/Hermes-3-Llama-3.1-70B"
		}
		return "", ci, fmt.Errorf("HTTP %d: %s", status, msg)
	}
	if len(parsed.Choices) == 0 {
		return "", ci, errors.New("no choices in response")
	}
	if parsed.Usage != nil {
		ci.Prompt, ci.Compl = parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens
		ci.Text = fmt.Sprintf(", %d+%d tok", ci.Prompt, ci.Compl)
	}
	choice := parsed.Choices[0]
	if fr := choice.FinishReason; fr != "" && fr != "stop" {
		ci.Text += ", finish=" + fr
	}
	content := choice.Message.Content
	if strings.TrimSpace(thinkRe.ReplaceAllString(content, "")) == "" {
		reasoning := strings.TrimSpace(choice.Message.ReasoningContent)
		if reasoning == "" {
			reasoning = strings.TrimSpace(content)
		}
		hint := "model spent all tokens THINKING and never wrote the reply"
		if reasoning != "" {
			hint += fmt.Sprintf(" (reasoning %d chars: %q)", len(reasoning), trim(reasoning, 160))
		}
		hint += "\n     FIX: raise MAX_TOKENS (e.g. 1024) or disable thinking with EXTRA_JSON in apitest.config.txt, or use a non-reasoning model (e.g. meta-llama/Llama-3.3-70B-Instruct)"
		return "", ci, errors.New(hint)
	}
	return content, ci, nil
}

// noThinkFields returns request fields that switch off "thinking" for known hybrid
// reasoning families, so the model writes the chat line instead of reasoning tokens.
func noThinkFields(model string) map[string]any {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "qwen3"), strings.Contains(m, "qwen-3"), strings.Contains(m, "qwen4"):
		// Qwen3 / 3.5 / 3.6 / 3.8 hybrid thinking (vLLM chat template kwarg)
		return map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}}
	case strings.Contains(m, "glm-4.5"), strings.Contains(m, "glm-4.6"), strings.Contains(m, "glm-4.7"), strings.Contains(m, "glm-5"), strings.Contains(m, "glm5"):
		return map[string]any{"thinking": map[string]any{"type": "disabled"}, "chat_template_kwargs": map[string]any{"enable_thinking": false}}
	case strings.Contains(m, "deepseek-v3.1"), strings.Contains(m, "deepseek-v3.2"), strings.Contains(m, "deepseek-v4"), strings.Contains(m, "deepseek-r"):
		return map[string]any{"chat_template_kwargs": map[string]any{"thinking": false, "enable_thinking": false}, "thinking": map[string]any{"type": "disabled"}}
	case strings.Contains(m, "kimi-k2.5"), strings.Contains(m, "kimi-k3"), strings.Contains(m, "kimi-k2-thinking"):
		return map[string]any{"thinking": map[string]any{"type": "disabled"}, "chat_template_kwargs": map[string]any{"thinking": false, "enable_thinking": false}}
	case strings.Contains(m, "gpt-oss"):
		return map[string]any{"reasoning_effort": "low"} // cannot be fully disabled
	case strings.Contains(m, "minimax-m"), strings.Contains(m, "mimo"), strings.Contains(m, "step-3"), strings.Contains(m, "nemotron"):
		return map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false, "thinking": false}}
	}
	return nil
}

func rawCall(cfg config, msgs []message, maxTokens int) (int, []byte, error) {
	fields := map[string]any{
		"model":       cfg.Model,
		"messages":    msgs,
		"max_tokens":  maxTokens,
		"temperature": cfg.Temperature,
	}
	if cfg.AutoNoThink {
		for k, v := range noThinkFields(cfg.Model) {
			fields[k] = v
		}
	}
	for k, v := range cfg.ExtraJSON { // explicit EXTRA_JSON always wins
		fields[k] = v
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	resp, err := (&http.Client{Timeout: cfg.Timeout}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// sanitizeLines applies the rules the real bot would enforce and returns the
// line(s) to send (1, or 2 when a double line was requested).
func sanitizeLines(raw string, double bool) ([]string, string) {
	text := strings.TrimSpace(thinkRe.ReplaceAllString(raw, ""))
	var warns []string
	if strings.Contains(raw, "<think>") {
		warns = append(warns, "model emitted <think> reasoning (stripped)")
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "\"'“”`"))
		l = strings.TrimLeft(l, "-•*0123456789. ")
		for _, prefix := range []string{"you:", "me:", "eva:", "bot:", "line 1:", "line 2:", "line1:", "line2:"} {
			if strings.HasPrefix(strings.ToLower(l), prefix) {
				l = strings.TrimSpace(l[len(prefix):])
			}
		}
		l = strings.ReplaceAll(l, "—", ", ")
		l = strings.ReplaceAll(l, " - ", ", ")
		l = strings.Join(strings.Fields(l), " ")
		l = strings.ReplaceAll(l, "!", "")
		l = trailingDotRe.ReplaceAllString(l, " $1")
		l = strings.TrimRight(strings.Join(strings.Fields(l), " "), ".!, ")
		if l != "" {
			lines = append(lines, strings.ToLower(l))
		}
	}
	if len(lines) == 0 {
		return []string{"(empty)"}, "empty reply -> bot would use a TXT fallback line"
	}
	want := 1
	if double {
		want = 2
	}
	if len(lines) > want {
		warns = append(warns, fmt.Sprintf("model wrote %d lines, kept %d", len(lines), want))
		lines = lines[:want]
	}
	for _, l := range lines {
		if len([]rune(l)) > 120 {
			warns = append(warns, "line too long (>120 chars) -> bot would fall back")
		}
		if n := len(strings.Fields(l)); n > 14 {
			warns = append(warns, fmt.Sprintf("%d words (target <=10)", n))
		}
		if forbiddenRe.MatchString(l) {
			warns = append(warns, "contains @ / link / ai-talk -> bot would fall back")
		}
		if strings.Count(l, "?") > 1 {
			warns = append(warns, "two questions in one line")
		}
	}
	return lines, strings.Join(warns, "; ")
}

// onlyQuestion: the line is nothing but a question (starts with a question word, ends with ?).
// "nothing much, u?" is an answer + question -> false. "what about u?" -> true.
func onlyQuestion(l string) bool {
	l = strings.TrimSpace(strings.ToLower(l))
	if !strings.HasSuffix(l, "?") {
		return false
	}
	f := strings.Fields(strings.Trim(l, "?"))
	if len(f) == 0 {
		return true
	}
	switch strings.Trim(f[0], ",.") {
	case "what", "how", "where", "why", "who", "when", "which", "wyd", "hbu", "wbu", "r", "are", "do", "did", "does", "u", "you", "can", "could", "would", "will", "is", "got", "any", "so", "and", "ur", "your", "whats", "what's", "hows", "how's", "wanna", "want", "ever", "have", "wya", "asl":
		return len(f) <= 7
	}
	return false
}

// parrotGram returns a 4-word run of the stranger's message that appears verbatim in the reply.
func parrotGram(user, reply string) string {
	clean := func(t string) []string {
		return strings.Fields(regexp.MustCompile(`[^a-z0-9' ]+`).ReplaceAllString(t, " "))
	}
	uw := clean(user)
	rw := " " + strings.Join(clean(reply), " ") + " "
	for i := 0; i+4 <= len(uw); i++ {
		g := strings.Join(uw[i:i+4], " ")
		if strings.Contains(rw, " "+g+" ") {
			return g
		}
	}
	return ""
}

// polish removes bot-tells that are easier to fix than to retry:
// same emoji twice, emoji in two of the last three replies, "lol"/"haha" opener two replies in a row.
func polish(lines []string, history []message) []string {
	var recent []string
	for i := len(history) - 1; i >= 0 && len(recent) < 3; i-- {
		if history[i].Role == "assistant" {
			recent = append(recent, history[i].Content)
		}
	}
	recentEmoji := false
	usedEmoji := map[rune]bool{}
	for _, m := range history {
		if m.Role == "assistant" {
			for _, r := range m.Content {
				if isEmoji(r) {
					usedEmoji[r] = true
				}
			}
		}
	}
	for _, r := range recent {
		for _, c := range r {
			if isEmoji(c) {
				recentEmoji = true
			}
		}
	}
	lastOpeners := map[string]bool{}
	for i, r := range recent {
		if i > 1 {
			break
		}
		if f := strings.Fields(r); len(f) > 0 {
			if w := strings.Trim(f[0], ",."); w == "lol" || w == "haha" || w == "lmao" || w == "omg" {
				lastOpeners[w] = true
			}
		}
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		var b strings.Builder
		kept := false
		for _, c := range l {
			if isEmoji(c) {
				if recentEmoji || usedEmoji[c] || kept {
					continue
				}
				kept = true
			}
			b.WriteRune(c)
		}
		l = strings.TrimRight(strings.TrimSpace(strings.Join(strings.Fields(b.String()), " ")), ".,! ")
		if f := strings.Fields(l); len(f) > 1 && lastOpeners[strings.Trim(f[0], ",.")] {
			l = strings.TrimSpace(strings.TrimLeft(strings.Join(f[1:], " "), ", "))
		}
		if l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return lines
	}
	return out
}

func isEmoji(r rune) bool {
	return r >= 0x1F300 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF || r == 0x2764 || r == 0xFE0F
}

func genderAge(low string) (string, int, bool) {
	for i, re := range ageRe {
		m := re.FindStringSubmatch(low)
		if len(m) < 2 {
			continue
		}
		var g, a string
		switch i {
		case 0, 2:
			g, a = m[1], m[2]
		case 1, 3:
			a, g = m[1], m[2]
		default:
			a = m[1]
		}
		switch g {
		case "male", "guy", "boy", "man", "dude":
			g = "m"
		case "female", "girl", "woman":
			g = "f"
		}
		n, err := strconv.Atoi(a)
		if err == nil && n >= 13 && n <= 99 {
			return g, n, true
		}
	}
	return "", 0, false
}

// -------------------------------------------------------------- config ---

func loadPersona(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("persona.txt: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return "", errors.New("persona.txt is empty")
	}
	return text, nil
}

// --------------------------------------------------------------- misc ---

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
