package main

import (
	"bufio"
	"encoding/gob"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	irc "github.com/fluffle/goirc/client"
)

var c *Chain
var numWords *int
var prefixLen *int
var server *string
var channel *string

var myNick *string

var mutex *sync.Mutex

var replyChannel chan string
var learnChannel chan string

func init() {
	rand.Seed(time.Now().UnixNano()) // Seed the random number generator.
	numWords = flag.Int("words", 100, "maximum number of words to print")
	prefixLen = flag.Int("prefix", 2, "prefix length in words")
	server = flag.String("server", "irc.efnet.org", "server to connect to (irc.something.net:6667)")
	channel = flag.String("channel", "#chatbotpurgatory", "channel to join")
	myNick = flag.String("nickname", "SVETSE", "nickname for the bot")

	mutex = &sync.Mutex{}

	flag.Parse() // Parse command-line flags.

	replyChannel = make(chan string)
	learnChannel = make(chan string)
}

// Prefix is a Markov chain prefix of one or more words.
type Prefix []string

// String returns the Prefix as a string (for use as a map key).
func (p Prefix) String() string {
	return strings.Join(p, " ")
}

// Shift removes the first word from the Prefix and appends the given word.
func (p Prefix) Shift(word string) {
	copy(p, p[1:])
	p[len(p)-1] = word
}

// Chain contains a map ("chain") of prefixes to a list of suffixes.
// A prefix is a string of prefixLen words joined with spaces.
// A suffix is a single word. A prefix can have multiple suffixes.
type Chain struct {
	MapChain  map[string][]string
	PrefixLen int
}

// NewChain returns a new Chain with prefixes of prefixLen words.
func NewChain(prefixLen int) *Chain {
	return &Chain{make(map[string][]string), prefixLen}
}

// Build reads text from the provided Reader and
// parses it into prefixes and suffixes that are stored in Chain.
func (c *Chain) Build(r io.Reader) bool {
	br := bufio.NewReader(r)
	p := make(Prefix, c.PrefixLen)
	for {
		var s string
		if _, err := fmt.Fscan(br, &s); err != nil {
			break
		}
		s = strings.ToLower(s)
		key := p.String()
		mutex.Lock()
		c.MapChain[key] = append(c.MapChain[key], s)
		mutex.Unlock()
		p.Shift(s)
	}
	return true
}

// Generate returns a string of at most n words generated from Chain.
func (c *Chain) Generate(n int) string {
	p := make(Prefix, c.PrefixLen)
	var words []string
	for i := 0; i < n; i++ {
		mutex.Lock()
		choices := c.MapChain[p.String()]
		mutex.Unlock()
		if len(choices) == 0 {
			break
		}
		next := choices[rand.Intn(len(choices))]
		words = append(words, next)
		p.Shift(next)
	}
	return strings.Join(words, " ")
}

func ircConfig() *irc.Config {
	cfg := irc.NewConfig(*myNick)
	cfg.SSL = false
	cfg.Server = *server
	cfg.NewNick = func(n string) string { return n + "^" }
	return cfg
}

func handlePrivMsg(conn *irc.Conn, line *irc.Line) {
	cleanText := ""
	if strings.Contains(line.Text(), *myNick) {
		// Reply if the text contains my nickname
		cleanText = strings.TrimPrefix(line.Text(), *myNick+": ")
		cleanText = strings.TrimPrefix(cleanText, *myNick+":")
		cleanText = strings.Replace(cleanText, *myNick, "", -1)
		//c.Build(strings.NewReader(cleanText))
		learnChannel <- cleanText
		replyChannel <- ""     // Send an empty request
		text := <-replyChannel // Get a reply back
		conn.Privmsg(*channel, text)
		log.Println(text)
		//log.Println(c.MapChain)
	} else {
		// Else just learn from the input
		//c.Build(strings.NewReader(cleanText))
		learnChannel <- line.Text()
	}
}

func learn() {
	for {
		text := <-learnChannel
		log.Printf("Learned the following: %s\n", text)
		_ = c.Build(strings.NewReader(text))
	}
}

func getReply() {
	for {
		<-replyChannel
		reply := c.Generate(*numWords)
		log.Printf("Replying with: %s\n", reply)
		replyChannel <- reply
	}
}

func saveBrain(f *os.File) {
	for {
		time.Sleep(time.Second * 10)
		log.Println("Saving brain...")
		enc := gob.NewEncoder(f)
		mutex.Lock()
		err := enc.Encode(c)
		mutex.Unlock()
		if err != nil {
			log.Printf("Could not save brain to disk: %s\n", err)
		}
	}
}

func main() {
	quit := make(chan bool)

	f, err := os.OpenFile("brain.gob", os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		panic("Could not open file")
	}

	dec := gob.NewDecoder(f)

	err = dec.Decode(&c)
	if err != nil {
		fmt.Printf("Could not load brain gob: %s\n", err)
		c = NewChain(*prefixLen) // Initialize a new Chain.
		fmt.Println("Generating new brain")
	}

	// Start goroutines
	go learn()
	go getReply()
	go saveBrain(f)

	client := irc.Client(ircConfig())
	client.HandleFunc(irc.CONNECTED, func(conn *irc.Conn, line *irc.Line) {
		conn.Join(*channel)
	})
	client.HandleFunc(irc.DISCONNECTED, func(conn *irc.Conn, line *irc.Line) {
		quit <- true
	})
	client.HandleFunc(irc.PRIVMSG, handlePrivMsg)

	err = client.Connect()
	if err != nil {
		log.Fatalf("Could not connect to IRC: %s", err)
	}

	for {
		<-quit
		os.Exit(0)
	}

}
