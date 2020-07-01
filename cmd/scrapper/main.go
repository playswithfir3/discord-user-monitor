package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/jszwec/csvutil"

	"github.com/tebeka/selenium"

	"github.com/spf13/pflag"
)

const discordLoginPage = "https://discord.com/login"

var (
	seleniumPort    = pflag.Int("selenium-port", 4444, "port of selenium server")
	seleniumBrowser = pflag.String("selenium-browser", "firefox", "browser to be used by selenium")

	discordLoadTime                = pflag.Int("d-load-time", 10, "time needed to load Discord page")
	discordEmail                   = pflag.String("d-email", "", "Discord email (used for login)")
	discordPassword                = pflag.String("d-password", "", "Discord password (used for login)")
	discordServerID                = pflag.String("d-server-id", "", "Discord server ID (from where to scrap data)")
	discordServerName              = pflag.String("d-server-name", "", "Discord server name (from where to scrap data)")
	discordUsername                = pflag.String("d-username", "", "Discord username (used to not include in output .csv file)")
	discordServerMaxScrolls        = pflag.IntP("d-server-max-scrolls", "s", 150, "Discord server maximum amount of scrolls to be done (10 for 100 users, 100 for 1000 users and etc)")
	discordServerScrollRefreshTime = pflag.IntP("d-server-scroll-refresh-time", "r", 300, "Time in milliseconds to wait after scrolling (higher value is better, lower value is faster scraping)")

	pathToOutputFile = pflag.StringP("output", "o", "users.csv", "Path to output file (in .csv format)")
)

// User struct represents a user with it's status in Discord
type User struct {
	Username string `csv:"username"`
	Status   string `csv:"status"`
	Type     string `csv:"type"` // user or bot

	StatusTime time.Time `csv:"status_time"` // time when user changed status
}

func main() {
	defer os.Exit(0)

	pflag.Parse()

	if *discordEmail == "" || *discordPassword == "" {
		pflag.Usage()
		os.Exit(1)
	}

	if *discordServerID == "" && *discordServerName == "" {
		pflag.Usage()
		os.Exit(1)
	}

	// check if output file exists, if no then create it
	var (
		outputFile *os.File
		err        error
	)

	outputFile, err = os.Create(*pathToOutputFile)
	if err != nil {
		log.Printf("Couldn't create output file: %v\n", err)
		runtime.Goexit()
	}

	// create new selenium web driver
	seleniumURL := fmt.Sprintf("http://localhost:%d/wd/hub", *seleniumPort)
	caps := selenium.Capabilities{"browserName": *seleniumBrowser}
	driver, err := selenium.NewRemote(caps, seleniumURL)
	if err != nil {
		log.Fatalf("Create new selenium driver: %v\n", err)
	}
	defer driver.Close()

	// deal a CTRL + C signal
	go func() {
		log.Println("Waiting for SIGINT signal")
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit

		err = driver.Close()
		if err != nil {
			log.Printf("Closing selenium web driver: %v\n", err)
			runtime.Goexit()
		}
	}()

	log.Println("Scrapper is running.")

	// navigate to discord login page
	err = driver.Get(discordLoginPage)
	if err != nil {
		log.Fatalf("Navigating to Discord login page: %v\n", err)
	}

	// perform login
	time.Sleep(time.Duration(*discordLoadTime) * time.Second)

	// fill email field
	emailField, err := driver.FindElement(selenium.ByXPATH, "/html/body/div/div[2]/div/div[2]/div/div/form/div/div/div[1]/div[3]/div[1]/div/input")
	if err != nil {
		log.Printf("Finding email field: %v\n", err)
		runtime.Goexit()
	}

	err = emailField.SendKeys(*discordEmail)
	if err != nil {
		log.Printf("Filling email field: %v\n", err)
		runtime.Goexit()
	}

	// fill password field
	passwordField, err := driver.FindElement(selenium.ByXPATH, "/html/body/div/div[2]/div/div[2]/div/div/form/div/div/div[1]/div[3]/div[2]/div/input")
	if err != nil {
		log.Printf("Finding password field: %v\n", err)
		runtime.Goexit()
	}

	err = passwordField.SendKeys(*discordPassword)
	if err != nil {
		log.Printf("Filling password field: %v\n", err)
		runtime.Goexit()
	}

	// click submit button
	submitBtn, err := driver.FindElement(selenium.ByCSSSelector, `button[type="submit"]`)
	if err != nil {
		log.Printf("Finding submit button: %v\n", err)
		runtime.Goexit()
	}

	err = submitBtn.Click()
	if err != nil {
		log.Printf("Clicking submit button: %v\n", err)
		runtime.Goexit()
	}

	log.Println("Logged in successfully !")
	time.Sleep(time.Duration(*discordLoadTime) * time.Second) // wait for page to load

	// find and click server link
	if *discordServerName != "" { // find by name
		serverLink, err := driver.FindElement(selenium.ByCSSSelector, fmt.Sprintf(`a[aria-label="%s"`, *discordServerName))
		if err != nil {
			log.Printf("Finding server link: %v\n", err)
			runtime.Goexit()
		}

		err = serverLink.Click()
		if err != nil {
			log.Printf("Clicking server link: %v\n", err)
			runtime.Goexit()
		}
	} else { // find by id
		serverLink, err := driver.FindElement(selenium.ByCSSSelector, fmt.Sprintf(`a[href*="%s"`, *discordServerID))
		if err != nil {
			log.Printf("Finding server link: %v\n", err)
			runtime.Goexit()
		}

		err = serverLink.Click()
		if err != nil {
			log.Printf("Clicking server link: %v\n", err)
			runtime.Goexit()
		}
	}

	time.Sleep(2 * time.Second) // wait until clicked server is loaded

	// scrap user data using right bar
	log.Println("Scrapping user data in progress...")
	usernameStatuses := make(map[string]User, 0) // collect all usernames and statuses into map
	// so basically here, we iterate through right bar of Discord, where all users are located
	// because of lazy loading, we scroll by 500px after each iteration and then
	// add new and old users to map
	i := 0
	for i < *discordServerMaxScrolls {
		layoutElems, err := driver.FindElements(selenium.ByCSSSelector, `div[class*="member"] > div[class*="layout"]`)
		if err != nil {
			log.Printf("Finding user layouts: %v\n", err)
			runtime.Goexit()
		}

		for _, layout := range layoutElems {
			var username, status, userType string

			// find avatar class, username and status are contained here
			user, err := layout.FindElement(selenium.ByCSSSelector, `div[class*="avatar"] > div[class*="wrapper"]`)
			if err != nil {
				//log.Printf("Finding user icons: %v\n", err)
				continue
			}

			// find content class, bot account names are container here
			_, err = layout.FindElement(selenium.ByCSSSelector, `div[class*="content"] > div[class*="nameAndDecorators"] > span[class*="botTag"]`)
			if err != nil { // if error happened then type is user
				userType = "user"
			} else { // else type is bot
				userType = "bot"
			}

			// retrieve each username and status from aria-label attribute and avatar class
			info, err := user.GetAttribute("aria-label")
			if err != nil {
				//log.Printf("Getting status of user: %v\n", err)
				continue
			}

			// if info doesn't contain ',', means user is offline
			if strings.ContainsAny(info, ",") {
				// separate username and status, eg: 'bejaneps, Online'
				temp := strings.Split(info, ",")

				username = temp[0]
				status = temp[1][1:] // skip space
			} else {
				username = info
				status = "Offline"
			}

			// if user supplied his/her username then omit it from output
			if *discordUsername != "" {
				if strings.EqualFold(*discordUsername, username) {
					continue
				}
			}

			// add user to temporary map
			usernameStatuses[username] = User{
				Username:   username,
				Status:     status,
				Type:       userType,
				StatusTime: time.Now(),
			}
		}

		// scroll right bar for 700px each iteration
		if i > 0 {
			// get right bar scroll element
			rightBar, err := driver.FindElement(selenium.ByCSSSelector, `div[class*="membersWrap"] > div[class*="scrollerBase"]`)
			if err != nil {
				log.Printf("Finding right scroll bar: %v\n", err)
				runtime.Goexit()
			}

			// scroll user icons to top by some amount of pixels
			temp := make([]interface{}, 1)
			temp = append(temp, rightBar)
			_, err = driver.ExecuteScript("arguments[1].scrollTop += 700", temp)
			if err != nil {
				log.Printf("Scrolling window vertically: %v\n", err)
				runtime.Goexit()
			}
		}
		time.Sleep(time.Millisecond * time.Duration(*discordServerScrollRefreshTime))

		i++
	}
	log.Println("Scrapping is done !")

	// add all users to output file
	usersSlice := make([]User, 0)
	for _, v := range usernameStatuses {
		usersSlice = append(usersSlice, v)
	}

	w := csv.NewWriter(outputFile)
	err = csvutil.NewEncoder(w).Encode(&usersSlice)
	if err != nil {
		log.Printf("Couldn't add users to output file: %v\n", err)

	}
}

/* Snippets

// _, err = os.Stat(*pathToOutputFile)
	// if errors.Is(err, os.ErrNotExist) {
	// 	outputFile, err = os.Create(*pathToOutputFile)
	// 	if err != nil {
	// 		log.Printf("Couldn't create output file: %v\n", err)
	// 		runtime.Goexit()
	// 	}
	// } else {
	// 	outputFile, err = os.Open(*pathToOutputFile)
	// 	if err != nil {
	// 		log.Printf("Couldn't open output file: %v\n", err)
	// 		runtime.Goexit()
	// 	}
	// }

*/