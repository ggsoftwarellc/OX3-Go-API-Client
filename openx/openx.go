package openx

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/mrjones/oauth"
	log "github.com/sirupsen/logrus"
)

// oauth global consumer (private)
var consumer *oauth.Consumer

const (
	version          = "0.0.1"
	requestTokenURL  = "https://sso.openx.com/api/index/initiate"
	accessTokenURL   = "https://sso.openx.com/api/index/token"
	authorizationURL = "https://sso.openx.com/login/process"
	apiPath          = "/ox/4.0/"
	callBack         = "oob"
)

// Client holds all the auth data, no point in exposing it to the user though
type Client struct {
	domain          string
	realm           string
	scheme          string
	consumerKey     string
	consumerSecrect string
	email           string
	password        string
	apiPath         string
	timeOut         int
	session         *http.Client
}

// Get is simailiar to the normal Go *http.client.Get,
// except string parameters can be passed in the url or the as a map[string]interface{}
func (c *Client) Get(url string, urlParms map[string]interface{}) (res *http.Response, err error) {
	url = c.resolveURL(url)
	if urlParms != nil {
		p := "?"
		for key, value := range urlParms {
			var v string
			switch value.(type) {
			case string:
				v = value.(string)
			case int:
				v = strconv.Itoa(value.(int))
			case float64:
				v = strconv.FormatFloat(value.(float64), 'f', -1, 64)
			case bool:
				v = strconv.FormatBool(value.(bool))
			default:
				log.Fatalln("The value entered %v must be of type string, int, float64, or bool")
			}
			p += key + "=" + v + "&"
		}
		url += p[:len(p)-1]
	}
	res, err = c.session.Get(url)
	return
}

// Delete creates a delete request
func (c *Client) Delete(url string, data io.Reader) (res *http.Response, err error) {
	request, err := http.NewRequest("DELETE", c.resolveURL(url), data)
	if err != nil {
		log.Fatalf("Could not create DELETE requests: %v", err)
	}
	res, err = c.session.Do(request)
	return
}

// Options is a wrapper for a GET request that has the /options endpoint already passed in
func (c *Client) Options(url string) (res *http.Response, err error) {
	if !strings.Contains(url, "/options") {
		url = path.Join("/options", url)
	}
	res, err = c.session.Get(c.resolveURL(url))
	return
}

// Put creates a put request
func (c *Client) Put(url string, data io.Reader) (res *http.Response, err error) {
	request, err := http.NewRequest("PUT", c.resolveURL(url), data)
	if err != nil {
		log.Fatalf("Could not create PUT requests: %v", err)
	}
	res, err = c.session.Do(request)
	return
}

// Post is a wrapper for the basic Go *http.client.Post, however content type is automatically set to application/json
func (c *Client) Post(url string, data io.Reader) (res *http.Response, err error) {
	res, err = c.session.Post(c.resolveURL(url), "application/json", data)
	return
}

// PostForm is a wrapper for the basic Go *http.client.PostForm
func (c *Client) PostForm(url string, data url.Values) (res *http.Response, err error) {
	res, err = c.session.PostForm(c.resolveURL(url), data)
	return
}

// LogOff sets the created session to an empty http.client
func (c *Client) LogOff() (res *http.Response, err error) {
	// set the session to an empty struct to clear auth information
	c.session = &http.Client{}
	return
}

func (c *Client) resolveURL(endpoint string) (u string) {
	rawURL, err := url.Parse(endpoint)
	if err != nil {
		log.Fatalln("Could not parse endpoint: %v", err)
	}
	if rawURL.Scheme == "" {
		u = fmt.Sprintf("%s://", c.scheme) + path.Join(c.domain, c.apiPath, rawURL.Path)
	}
	return
}

func (c *Client) getAccessToken(debug bool) (accessToken *oauth.AccessToken, err error) {
	requestToken, requestURL, err := consumer.GetRequestTokenAndUrl(callBack)
	if err != nil {
		err = fmt.Errorf("Requests token could not be generated:\n %v", err)
		return
	}
	if debug {
		log.Info("Requests Token generated")
	}

	// auth into openx
	request := http.Client{}
	urlData := url.Values{}
	urlData.Set("email", c.email)
	urlData.Set("password", c.password)
	urlData.Set("oauth_token", requestToken.Token)
	resp, err := request.PostForm(requestURL, urlData)
	if err != nil {
		err = fmt.Errorf("Could not get authorization token:\n %v", err)
		return
	}
	if debug {
		log.Info("Getting auth token")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("Could not get authorization status returned:\n %v", err)
		return
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("Could not read body: %v", err)
		return
	}
	// parse the response, should contain oauth_verifier
	raw, err := url.Parse(string(body))
	if err != nil {
		err = fmt.Errorf("Could not parse url: %v", err)
		return
	}
	authInfo := raw.Query()
	var oauthVerifier string
	if _, ok := authInfo["oauth_verifier"]; !ok {
		err = fmt.Errorf("oauth_verifier key not in response:\n %v", err)
		return
	}
	oauthVerifier = authInfo["oauth_verifier"][0]

	// use oauth_verifier to get access_token
	accessToken, err = consumer.AuthorizeToken(requestToken, oauthVerifier)
	if err != nil {
		err = fmt.Errorf("Access token could not be generated:\n %v", err)
		return
	}
	if debug {
		log.Info("Access Token generated")
	}
	return accessToken, nil
}

// NewClient creates the basic Openx3 *Client via oauth1
func NewClient(domain, realm, consumerKey, consumerSecrect, email, password string, debug bool) (*Client, error) {
	if strings.TrimSpace(domain) == "" {
		return &Client{}, fmt.Errorf("Domain cannot be emtpy")
	}
	if strings.TrimSpace(realm) == "" {
		return &Client{}, fmt.Errorf("Realm cannot be emtpy")
	}
	if strings.TrimSpace(consumerKey) == "" {
		return &Client{}, fmt.Errorf("Consumer key cannot be emtpy")
	}
	if strings.TrimSpace(consumerSecrect) == "" {
		return &Client{}, fmt.Errorf("Consumer secrect cannot be emtpy")
	}
	if strings.TrimSpace(email) == "" {
		return &Client{}, fmt.Errorf("email cannot be emtpy")
	}
	if strings.TrimSpace(password) == "" {
		return &Client{}, fmt.Errorf("password cannot be emtpy")
	}

	// create base client default to http
	c := &Client{
		domain:          domain,
		realm:           realm,
		consumerKey:     consumerKey,
		consumerSecrect: consumerSecrect,
		apiPath:         apiPath,
		email:           email,
		password:        password,
		scheme:          "http",
	}

	// create oauth consumer
	consumer = oauth.NewConsumer(c.consumerKey, c.consumerSecrect, oauth.ServiceProvider{
		RequestTokenUrl:   requestTokenURL,
		AuthorizeTokenUrl: authorizationURL,
		AccessTokenUrl:    accessTokenURL,
		HttpMethod:        "POST",
	})
	consumer.Debug(debug)

	accessToken, err := c.getAccessToken(debug)
	if err != nil {
		return &Client{}, fmt.Errorf("Access token could not be generated:\n %v", err)
	}

	// create a cookie jar to add the access token to
	if debug {
		log.Info("Creating cookiejar")
	}
	cj, err := cookiejar.New(nil)
	if err != nil {
		return &Client{}, fmt.Errorf("Cookiejar could not be created %v", err)
	}

	// clean up entered domain just incase user passes in a domain in a way I'm not ready for
	r := strings.NewReplacer(
		"www.", "",
		"http://", "",
		"https://", "",
		"//", "",
		"/", "",
	)

	c.domain = r.Replace(c.domain)
	// format the domain
	base, err := url.Parse(fmt.Sprintf("%s://www.%s", c.scheme, c.domain))
	if err != nil {
		return &Client{}, fmt.Errorf("Domain could not be parsed to type url %v", err)
	}

	if debug {
		log.Info("Setting openx3_access_token in cookie jar")
	}
	// create auth cookie
	var cookies []*http.Cookie
	cookie := &http.Cookie{
		Name:   "openx3_access_token",
		Value:  accessToken.Token,
		Path:   "/",
		Domain: c.domain,
		Secure: false,
		// HttpOnly: false,
	}
	cookies = append(cookies, cookie)
	cj.SetCookies(base, cookies)

	// create authenticated session
	if debug {
		log.Info("Creating oauth1 session")
	}
	session, err := consumer.MakeHttpClient(accessToken)
	if err != nil {
		return &Client{}, fmt.Errorf("Could not create client %v", err)
	}
	session.Jar = cj
	c.session = session
	return c, nil
}

// NewClientFromFile parses a JSON file to grab your Openx Credentials
func NewClientFromFile(filePath string, debug bool) (*Client, error) {
	credentials := struct {
		Domain          string `json:"domain"`
		Realm           string `json:"realm"`
		ConsumerKey     string `json:"consumer_key"`
		ConsumerSecrect string `json:"consumer_secrect"`
		Email           string `json:"email"`
		Password        string `json:"password"`
	}{}
	contents, err := ioutil.ReadFile(filePath)
	if err != nil {
		return &Client{}, fmt.Errorf("Could not read %s:\n %v", filePath, err)
	}
	err = json.Unmarshal(contents, &credentials)
	if err != nil {
		return &Client{}, fmt.Errorf("Could not load bytes into struct:\n %v", err)
	}

	if strings.TrimSpace(credentials.Domain) == "" {
		return &Client{}, fmt.Errorf("Domain cannot be emtpy")
	}
	if strings.TrimSpace(credentials.Realm) == "" {
		return &Client{}, fmt.Errorf("Realm cannot be emtpy")
	}
	if strings.TrimSpace(credentials.ConsumerKey) == "" {
		return &Client{}, fmt.Errorf("Consumer key cannot be emtpy")
	}
	if strings.TrimSpace(credentials.ConsumerSecrect) == "" {
		return &Client{}, fmt.Errorf("Consumer secrect cannot be emtpy")
	}
	if strings.TrimSpace(credentials.Email) == "" {
		return &Client{}, fmt.Errorf("email cannot be emtpy")
	}
	if strings.TrimSpace(credentials.Password) == "" {
		return &Client{}, fmt.Errorf("password cannot be emtpy")
	}

	// create base client default to http
	c := &Client{
		domain:          credentials.Domain,
		realm:           credentials.Realm,
		consumerKey:     credentials.ConsumerKey,
		consumerSecrect: credentials.ConsumerSecrect,
		apiPath:         apiPath,
		email:           credentials.Email,
		password:        credentials.Password,
		scheme:          "http",
	}

	// create oauth consumer
	consumer = oauth.NewConsumer(c.consumerKey, c.consumerSecrect, oauth.ServiceProvider{
		RequestTokenUrl:   requestTokenURL,
		AuthorizeTokenUrl: authorizationURL,
		AccessTokenUrl:    accessTokenURL,
		HttpMethod:        "POST",
	})
	consumer.Debug(debug)

	accessToken, err := c.getAccessToken(debug)
	if err != nil {
		return &Client{}, fmt.Errorf("Access token could not be generated:\n %v", err)
	}

	// create a cookie jar to add the access token to
	if debug {
		log.Info("Creating cookiejar")
	}
	cj, err := cookiejar.New(nil)
	if err != nil {
		return &Client{}, fmt.Errorf("Cookiejar could not be created %v", err)
	}

	// clean up entered domain just incase user passes in a domain in a way I'm not ready for
	r := strings.NewReplacer(
		"www.", "",
		"http://", "",
		"https://", "",
		"//", "",
		"/", "",
	)

	c.domain = r.Replace(c.domain)
	// format the domain
	base, err := url.Parse(fmt.Sprintf("%s://www.%s", c.scheme, c.domain))
	if err != nil {
		return &Client{}, fmt.Errorf("Domain could not be parsed to type url %v", err)
	}

	if debug {
		log.Info("Setting openx3_access_token in cookie jar")
	}
	// create auth cookie
	var cookies []*http.Cookie
	cookie := &http.Cookie{
		Name:   "openx3_access_token",
		Value:  accessToken.Token,
		Path:   "/",
		Domain: c.domain,
		Secure: false,
		// HttpOnly: false,
	}
	cookies = append(cookies, cookie)
	cj.SetCookies(base, cookies)

	// create authenticated session
	if debug {
		log.Info("Creating oauth1 session")
	}
	session, err := consumer.MakeHttpClient(accessToken)
	if err != nil {
		return &Client{}, fmt.Errorf("Could not create client %v", err)
	}
	session.Jar = cj
	c.session = session
	return c, nil
}

// CreateConfigFileTemplate creates a templated json file used in NewClientFromFile.
// Otherwise the file format for NewClientFromFile is
/*
  {
	"domain": "enter domain",
	"realm": "enter realm",
	"consumer_key": "enter key",
	"consumer_secrect": "enter secrect key",
	"email": "enter email",
	"password": "enter password"
  }
*/
// the fileCreationPath is returned incase a path is needed
func CreateConfigFileTemplate(fileCreationPath string) string {
	configFile := `
	{
		"domain": "enter domain",
		"realm": "enter realm",
		"consumer_key": "enter key",
		"consumer_secrect": "enter secrect key",
		"email": "enter email",
		"password": "enter password"
	}
	`
	if !strings.Contains(fileCreationPath, ".json") {
		fileCreationPath = path.Join(fileCreationPath, "openx_config.json")
	}

	f, err := os.Create(fileCreationPath)
	if err != nil {
		log.Fatalf("Could not create the file:\n %v", err)
	}
	defer f.Close()
	_, err = f.WriteString(configFile)
	if err != nil {
		log.Fatalf("Could not write data to %s:\n %v", fileCreationPath, err)
	}
	log.Infof("File was created: %s\n", fileCreationPath)
	return fileCreationPath
}
