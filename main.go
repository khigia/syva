package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	//"github.com/bugagazavr/go-gitlab-client"
	"github.com/khigia/go-gitlab-client"
	"io/ioutil"
	"net/http"

	"gopkg.in/yaml.v2"
	"log"
	"strconv"
)

type GlMrEvent struct {
	ObjectKind       string              `json:"object_kind"`
	ObjectAttributes *GlObjectAttributes `json:"object_attributes,omitempty"`
}
type GlObjectAttributes struct {
	Action       string     `json:"action,omitempty"`
	AuthorId     int        `json:"author_id,omitempty"`
	AssigneeId   int        `json:"assignee_id,omitempty"`
	Description  string     `json:"description,omitempty"` // in the doc, but not populated by gitlab API?
	IId          int        `json:"iid,omitempty"`
	MergeStatus  string     `json:"merge_status,omitempty"`
	SourceBranch string     `json:"source_branch,omitempty"`
	State        string     `json:"state,omitempty"`
	Target       *GlProject `json:"target,omitempty"`
	TargetBranch string     `json:"target_branch,omitempty"`
	Url          string     `json:"url,omitempty"` // in the doc, but not populated by gitlab API?
}
type GlProject struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type GlRepository struct {
	Name     string `json:"name,omitempty"`
	Homepage string `json:"homepage,omitempty"`
}
type GlPushEvent struct {
	Before     string        `json:"before,omitempty"`
	Ref        string        `json:"ref,omitempty"`
	Repository *GlRepository `json:"repository,omitempty"`
}

type Config struct {
	Port int

	Slack struct {
		IncomingWebhook string `incoming_webhook`
		Username        string
		IconEmoji       string `icon_emoji`
		Channel         string
	}

	Gitlab struct {
		Host    string
		ApiPath string `api_path`
		Token   string
	}
}

// Global var ... quick, easy and unmaintainable!
var config *Config
var gitlabUser map[int]string // id => username
var gitlab *gogitlab.Gitlab

func main() {
	log.Printf("Public cheri, mon amour")

	help := flag.Bool("help", false, "Show usage")
	configfile := flag.String("config", "config.yaml", "Config file name")

	flag.Usage = func() {
		fmt.Printf("Usage:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *help == true {
		flag.Usage()
		return
	}

	config = &Config{Port: 8080}

	file, err := ioutil.ReadFile(*configfile)
	if err != nil {
		log.Fatalf("Config file error: %v", err)
	}
	err = yaml.Unmarshal(file, &config)
	if err != nil {
		log.Fatalf("Config read error: %v", err)
	}
	d, err := yaml.Marshal(&config)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	log.Printf("Configuration:\n%s\n", string(d))

	// TODO update user list on gitlab admin event (and move out of global!)
	gitlab = gogitlab.NewGitlab(config.Gitlab.Host, config.Gitlab.ApiPath, config.Gitlab.Token)
	// TODO loop till all users (this works only if there are less than 100 users)
	users, err := gitlab.Users(0, 100)
	if err != nil {
		log.Printf("no access to users definition: %v", err)
	}
	gitlabUser = make(map[int]string)
	for _, user := range users {
		log.Printf("User id[%d] username[%s]", user.Id, user.Username)
		gitlabUser[user.Id] = user.Username
	}

	http.HandleFunc("/cmd/mr/list", cmdMrList)
	http.HandleFunc("/gl/mr", glMrEvent)
	http.HandleFunc("/gl/push", glPushEvent)
	http.ListenAndServe(":"+strconv.Itoa(config.Port), nil)
}

func slack(text string, author string) {
	log.Println("Slacking...")

	b, err := json.Marshal(map[string]string{
		"text":       text,
		"username":   author, // config.Slack.Username,
		"icon_emoji": config.Slack.IconEmoji,
		"channel":    config.Slack.Channel})
	if err != nil {
		log.Printf("json encode: %v", err)
		return
	}

	buf := bytes.NewReader(b)
	// TODO use a client (save some TCP)
	_, err = http.Post(config.Slack.IncomingWebhook, "text", buf)
	if err != nil {
		log.Println(err.Error())
		// TODO can we re-try (useful on temporary timeout)
		return
	}
	log.Printf("...slacked: %s", string(b))
}

func cmdMrList(w http.ResponseWriter, r *http.Request) {
	// TODO repsonse with "ok, processing" message
	go _cmdMrList()
	return
}

func _cmdMrList() {
	id := "208"
	mrs, err := gitlab.ProjectMergeRequests(id, 0, 30, "opened")
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	msg := ""
	for _, mr := range mrs {
		// author := ""
		// if mr.Author != nil {
		// 	author = mr.Author.Username
		// }
		assignee := "???"
		if mr.Assignee != nil {
			assignee = mr.Assignee.Username
		}
		url := fmt.Sprintf(
			"%s/%s/%s/merge_requests/%d",
			config.Gitlab.Host,
			"p",
			"higgs",
			mr.IId)
		msg += fmt.Sprintf("MR <%s> *%s* _assignee_:%s\n",
			url,
			mr.SourceBranch,
			assignee)
	}
	log.Println(msg)
	slack(msg, config.Slack.Username)
}

func glMrEvent(w http.ResponseWriter, r *http.Request) {
	payload, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Println(err.Error())
		return
	}
	log.Println(string(payload))

	var mr GlMrEvent
	err = json.Unmarshal(payload, &mr)
	if err != nil {
		log.Println(err.Error())
		return
	}
	if mr.ObjectKind != "merge_request" {
		log.Println("Not a merge request:" + string(payload))
		return
	}
	if mr.ObjectAttributes == nil {
		log.Println("Bad merge request format:" + string(payload))
		return
	}
	url := mr.ObjectAttributes.Url
	if url == "" {
		log.Println("Hulk sad!")
		if mr.ObjectAttributes.Target == nil {
			log.Println("Desperate house!")
			url = "http://gitlab/is/stupid/2.0"
		} else {
			// TODO URL builder
			url = fmt.Sprintf(
				"%s/%s/%s/merge_requests/%d",
				config.Gitlab.Host,
				mr.ObjectAttributes.Target.Namespace,
				mr.ObjectAttributes.Target.Name,
				mr.ObjectAttributes.IId)
		}
	}
	// TODO if we are smart and download state at start,
	// we could detect state change etc

	state := mr.ObjectAttributes.State

	author, ok := gitlabUser[mr.ObjectAttributes.AuthorId]
	sender := author
	if !ok {
		log.Printf("unknown_author_id[%d]", mr.ObjectAttributes.AuthorId)
		author = strconv.Itoa(mr.ObjectAttributes.AuthorId)
	}
	author = " (_by " + author + "_)"

	assignee, ok := gitlabUser[mr.ObjectAttributes.AssigneeId]
	if !ok {
		log.Printf("unknown_assignee_id[%d]", mr.ObjectAttributes.AssigneeId)
		assignee = "" // strconv.Itoa(mr.ObjectAttributes.AssigneeId)
		author = ""
	} else {
		sender = assignee
		if state == "opened" {
			state = "assigned"
		}
	}

	description := ""
	if mr.ObjectAttributes.Action == "open" && mr.ObjectAttributes.State != "merged" {
		description = " " + mr.ObjectAttributes.Description
	}

	go slack(fmt.Sprintf(
		"<%s> *%s*%s %s%s",
		url,
		mr.ObjectAttributes.SourceBranch,
		author,
		// mr.ObjectAttributes.TargetBranch,
		state,
		description), sender)
}

func glPushEvent(w http.ResponseWriter, r *http.Request) {
	payload, err := ioutil.ReadAll(r.Body) // not efficient :)
	if err != nil {
		log.Println(err.Error())
		return
	}
	log.Println(string(payload))

	// var push GlPushEvent
	// err = json.Unmarshal(payload, &push)
	// if err != nil {
	// 	fmt.Println(err.Error())
	// 	return
	// }

	// slack(`Gitlab push on <` + push.Repository.Homepage + `>`)
}

// TODO when we get slack outgoing webhook setup, we can implement command
//      to interact with gitlab from slack
// var config Config
// json.Unmarshal(file, &config)
// fmt.Printf("Results: %+v\n", config)

// var gitlab *gogitlab.Gitlab

// gitlab = gogitlab.NewGitlab(config.Host, config.ApiPath, config.Token)

// startedAt := time.Now()
// defer func() {
// 	fmt.Printf("processed in %v\n", time.Now().Sub(startedAt))
// }()

// fmt.Println("Fetching projects…")

// projects, err := gitlab.Projects(1, 100)
// if err != nil {
// 	fmt.Println(err.Error())
// 	return
// }

// for _, project := range projects {
// 	fmt.Printf("> %6d | %s | %s\n", project.Id, project.PathWithNamespace, project.Name)
// }

// mrs, err := gitlab.ProjectMergeRequests("208", 0, 30, "opened")
// if err != nil {
// 	fmt.Println(err.Error())
// 	return
// }

// for _, mr := range mrs {
// 	author := ""
// 	if mr.Author != nil {
// 		author = mr.Author.Username
// 	}
// 	assignee := ""
// 	if mr.Assignee != nil {
// 		assignee = mr.Assignee.Username
// 	}
// 	fmt.Printf("  %s -> %s [%s] author[%s] assignee[%s]\n",
// 		mr.SourceBranch, mr.TargetBranch, mr.State,
// 		author, assignee)
// }
