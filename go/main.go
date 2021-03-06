package main

import (
	"bytes"
	"crypto/ecdsa"
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"

	"github.com/dgrijalva/jwt-go"
	"github.com/go-sql-driver/mysql"
	"github.com/gorilla/sessions"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4/middleware"
	gommonLog "github.com/labstack/gommon/log"
	"golang.org/x/sync/singleflight"
)

var group singleflight.Group

type omIsuConditionListT struct {
	M sync.Mutex
	V []*IsuCondition
}

var omIsuConditionList omIsuConditionListT

func (o *omIsuConditionListT) Get() []*IsuCondition {
	o.M.Lock()
	v := o.V
	o.V = []*IsuCondition{}
	defer o.M.Unlock()
	return v
}

func (o *omIsuConditionListT) Set(v []*IsuCondition) {
	o.M.Lock()
	o.V = append(o.V, v...)
	o.M.Unlock()
}

type omIsuT struct {
	M sync.RWMutex
	V map[string]*Isu
}

var omIsu omIsuT

func (o *omIsuT) Get(jiaIsuUUID, jiaUserID string) (*Isu, bool) {
	o.M.RLock()
	v, ok := o.V[fmt.Sprintf("%s-%s", jiaIsuUUID, jiaUserID)]
	o.M.RUnlock()
	return v, ok
}

func (o *omIsuT) Set(v *Isu) {
	o.M.Lock()
	o.V[fmt.Sprintf("%s-%s", v.JIAIsuUUID, v.JIAUserID)] = v
	o.M.Unlock()
}

type omIsu2T struct {
	M sync.RWMutex
	V map[string]*Isu
}

var omIsu2 omIsu2T

func (o *omIsu2T) Get(jiaIsuUUID string) (*Isu, bool) {
	o.M.RLock()
	v, ok := o.V[jiaIsuUUID]
	o.M.RUnlock()
	return v, ok
}

func (o *omIsu2T) Set(v *Isu) {
	o.M.Lock()
	o.V[v.JIAIsuUUID] = v
	o.M.Unlock()
}

const (
	sessionName                 = "isucondition_go"
	conditionLimit              = 20
	frontendContentsPath        = "../public"
	jiaJWTSigningKeyPath        = "../ec256-public.pem"
	defaultIconFilePath         = "../NoImage.jpg"
	defaultJIAServiceURL        = "http://localhost:5000"
	mysqlErrNumDuplicateEntry   = 1062
	conditionLevelInfo          = "info"
	conditionLevelWarning       = "warning"
	conditionLevelCritical      = "critical"
	scoreConditionLevelInfo     = 3
	scoreConditionLevelWarning  = 2
	scoreConditionLevelCritical = 1
)

var (
	db                  *sqlx.DB
	db2                 *sqlx.DB
	sessionStore        sessions.Store
	mySQLConnectionData *MySQLConnectionEnv

	jiaJWTSigningKey *ecdsa.PublicKey

	postIsuConditionTargetBaseURL string // JIA??????activate?????????????????????ISU???condition???????????????URL
)

type LatestIsuCondition struct {
	JIAIsuUUID string    `db:"jia_isu_uuid"`
	Timestamp  time.Time `db:"timestamp"`
	IsSitting  bool      `db:"is_sitting"`
	Condition  string    `db:"condition"`
	Message    string    `db:"message"`
	Level      string    `db:"level"`
}

type Config struct {
	Name string `db:"name"`
	URL  string `db:"url"`
}

type Isu struct {
	ID         int       `db:"id" json:"id"`
	JIAIsuUUID string    `db:"jia_isu_uuid" json:"jia_isu_uuid"`
	Name       string    `db:"name" json:"name"`
	Image      []byte    `db:"image" json:"-"`
	Character  string    `db:"character" json:"character"`
	JIAUserID  string    `db:"jia_user_id" json:"-"`
	CreatedAt  time.Time `db:"created_at" json:"-"`
	UpdatedAt  time.Time `db:"updated_at" json:"-"`

	Level     string    `db:"level" json:"-"`
	Timestamp time.Time `db:"timestamp" json:"-"`
}

type IsuFromJIA struct {
	Character string `json:"character"`
}

type GetIsuListResponse struct {
	ID                 int                      `json:"id"`
	JIAIsuUUID         string                   `json:"jia_isu_uuid"`
	Name               string                   `json:"name"`
	Character          string                   `json:"character"`
	LatestIsuCondition *GetIsuConditionResponse `json:"latest_isu_condition"`
}

type IsuCondition struct {
	ID         int       `db:"id"`
	JIAIsuUUID string    `db:"jia_isu_uuid"`
	Timestamp  time.Time `db:"timestamp"`
	IsSitting  bool      `db:"is_sitting"`
	Condition  string    `db:"condition"`
	Message    string    `db:"message"`
	CreatedAt  time.Time `db:"created_at"`
	Level      string    `db:"level"`
}

type MySQLConnectionEnv struct {
	Host     string
	Host2    string
	Port     string
	User     string
	DBName   string
	Password string
}

type InitializeRequest struct {
	JIAServiceURL string `json:"jia_service_url"`
}

type InitializeResponse struct {
	Language string `json:"language"`
}

type GetMeResponse struct {
	JIAUserID string `json:"jia_user_id"`
}

type GraphResponse struct {
	StartAt             int64           `json:"start_at"`
	EndAt               int64           `json:"end_at"`
	Data                *GraphDataPoint `json:"data"`
	ConditionTimestamps []int64         `json:"condition_timestamps"`
}

type GraphDataPoint struct {
	Score      int                  `json:"score"`
	Percentage ConditionsPercentage `json:"percentage"`
}

type ConditionsPercentage struct {
	Sitting      int `json:"sitting"`
	IsBroken     int `json:"is_broken"`
	IsDirty      int `json:"is_dirty"`
	IsOverweight int `json:"is_overweight"`
}

type GraphDataPointWithInfo struct {
	JIAIsuUUID          string
	StartAt             time.Time
	Data                GraphDataPoint
	ConditionTimestamps []int64
}

type GetIsuConditionResponse struct {
	JIAIsuUUID     string `json:"jia_isu_uuid"`
	IsuName        string `json:"isu_name"`
	Timestamp      int64  `json:"timestamp"`
	IsSitting      bool   `json:"is_sitting"`
	Condition      string `json:"condition"`
	ConditionLevel string `json:"condition_level"`
	Message        string `json:"message"`
}

type TrendResponse struct {
	Character string            `json:"character"`
	Info      []*TrendCondition `json:"info"`
	Warning   []*TrendCondition `json:"warning"`
	Critical  []*TrendCondition `json:"critical"`
}

type TrendCondition struct {
	ID        int   `json:"isu_id"`
	Timestamp int64 `json:"timestamp"`
}

type PostIsuConditionRequest struct {
	IsSitting bool   `json:"is_sitting"`
	Condition string `json:"condition"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

type JIAServiceRequest struct {
	TargetBaseURL string `json:"target_base_url"`
	IsuUUID       string `json:"isu_uuid"`
}

func getEnv(key string, defaultValue string) string {
	val := os.Getenv(key)
	if val != "" {
		return val
	}
	return defaultValue
}

func NewMySQLConnectionEnv() *MySQLConnectionEnv {
	return &MySQLConnectionEnv{
		Host:     getEnv("MYSQL_HOST", "127.0.0.1"),
		Host2:    getEnv("MYSQL_HOST2", "127.0.0.1"),
		Port:     getEnv("MYSQL_PORT", "3306"),
		User:     getEnv("MYSQL_USER", "isucon"),
		DBName:   getEnv("MYSQL_DBNAME", "isucondition"),
		Password: getEnv("MYSQL_PASS", "isucon"),
	}
}

func (mc *MySQLConnectionEnv) ConnectDB() (*sqlx.DB, *sqlx.DB, error) {
	dsn2 := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?parseTime=true&loc=Asia%%2FTokyo&interpolateParams=true", mc.User, mc.Password, mc.Host, mc.Port, mc.DBName)
	isu2, err := sqlx.Open("mysql", dsn2)
	if err != nil {
		return nil, nil, err
	}

	dsn3 := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?parseTime=true&loc=Asia%%2FTokyo&interpolateParams=true", mc.User, mc.Password, mc.Host2, mc.Port, mc.DBName)
	isu3, err := sqlx.Open("mysql", dsn3)
	if err != nil {
		return nil, nil, err
	}

	return isu2, isu3, nil
}

func init() {
	sessionStore = sessions.NewCookieStore([]byte(getEnv("SESSION_KEY", "isucondition")))

	key, err := ioutil.ReadFile(jiaJWTSigningKeyPath)
	if err != nil {
		log.Fatalf("failed to read file: %v", err)
	}
	jiaJWTSigningKey, err = jwt.ParseECPublicKeyFromPEM(key)
	if err != nil {
		log.Fatalf("failed to parse ECDSA public key: %v", err)
	}
}

type JSONSerializer struct{}

func (j *JSONSerializer) Serialize(c echo.Context, i interface{}, indent string) error {
	enc := json.NewEncoder(c.Response())
	return enc.Encode(i)
}

func (j *JSONSerializer) Deserialize(c echo.Context, i interface{}) error {
	err := json.NewDecoder(c.Request().Body).Decode(i)
	if ute, ok := err.(*json.UnmarshalTypeError); ok {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Unmarshal type error: expected=%v, got=%v, field=%v, offset=%v", ute.Type, ute.Value, ute.Field, ute.Offset)).SetInternal(err)
	} else if se, ok := err.(*json.SyntaxError); ok {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Syntax error: offset=%v, error=%v", se.Offset, se.Error())).SetInternal(err)
	}
	return err
}

func main() {
	var err error
	e := echo.New()
	e.JSONSerializer = &JSONSerializer{}
	//e.Debug = true
	e.Logger.SetLevel(gommonLog.ERROR)
	log.SetFlags(log.Lshortfile)
	logfile, err := os.OpenFile("/var/log/go.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		panic("cannnot open test.log:" + err.Error())
	}
	defer logfile.Close()
	log.SetOutput(logfile)
	e.Logger.SetOutput(logfile)
	log.Print("main")

	//e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.POST("/initialize", postInitialize)

	e.POST("/api/auth", postAuthentication)
	e.POST("/api/signout", postSignout)
	e.GET("/api/user/me", getMe)
	e.GET("/api/isu", getIsuList)
	e.POST("/api/isu", postIsu)
	e.GET("/api/isu/:jia_isu_uuid", getIsuID)
	e.GET("/api/isu/:jia_isu_uuid/icon", getIsuIcon)
	e.GET("/api/isu/:jia_isu_uuid/graph", getIsuGraph)
	e.GET("/api/condition/:jia_isu_uuid", getIsuConditions)
	e.GET("/api/trend", getTrend)

	e.POST("/api/condition/:jia_isu_uuid", postIsuCondition)

	e.GET("/", getIndex)
	e.GET("/isu/:jia_isu_uuid", getIndex)
	e.GET("/isu/:jia_isu_uuid/condition", getIndex)
	e.GET("/isu/:jia_isu_uuid/graph", getIndex)
	e.GET("/register", getIndex)
	e.Static("/assets", frontendContentsPath+"/assets")

	mySQLConnectionData = NewMySQLConnectionEnv()

	db, db2, err = mySQLConnectionData.ConnectDB()
	if err != nil {
		e.Logger.Fatalf("failed to connect db: %v", err)
		return
	}
	db.SetMaxOpenConns(10)
	defer db.Close()
	db2.SetMaxOpenConns(10)
	defer db2.Close()
	for {
		if err := db.Ping(); err == nil {
			break
		}
		time.Sleep(time.Second * 1)
	}
	for {
		if err := db2.Ping(); err == nil {
			break
		}
		time.Sleep(time.Second * 1)
	}

	postIsuConditionTargetBaseURL = os.Getenv("POST_ISUCONDITION_TARGET_BASE_URL")
	if postIsuConditionTargetBaseURL == "" {
		e.Logger.Fatalf("missing: POST_ISUCONDITION_TARGET_BASE_URL")
		return
	}

	omIsuConditionList.V = []*IsuCondition{}
	go loopPostIsuCondition()

	isuList := make([]*Isu, 0)
	if err := db.Select(&isuList, "SELECT * FROM isu"); err != nil {
		log.Println(err)
		return
	}

	omIsu = omIsuT{
		V: make(map[string]*Isu, 0),
	}
	omIsu2 = omIsu2T{
		V: make(map[string]*Isu, 0),
	}
	for _, v := range isuList {
		omIsu.Set(v)
		omIsu2.Set(v)
	}

	if os.Getenv("ISU") == "1" {
		socketFile := "/home/isucon/webapp/tmp/app.sock"
		os.Remove(socketFile)

		l, err := net.Listen("unix", socketFile)
		if err != nil {
			e.Logger.Fatal(err)
		}

		// go run????????????nginx???????????????????????????????????????????????????777???????????????ok
		err = os.Chmod(socketFile, 0777)
		if err != nil {
			e.Logger.Fatal(err)
		}

		e.Listener = l
		e.Logger.Fatal(e.Start(""))
	} else {
		serverPort := fmt.Sprintf(":%v", getEnv("SERVER_APP_PORT", "3000"))
		e.Logger.Fatal(e.Start(serverPort))
	}
}

func getSession(r *http.Request) (*sessions.Session, error) {
	session, err := sessionStore.Get(r, sessionName)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func getUserIDFromSession(c echo.Context) (string, int, error) {
	session, err := getSession(c.Request())
	if err != nil {
		return "", http.StatusInternalServerError, fmt.Errorf("failed to get session: %v", err)
	}
	_jiaUserID, ok := session.Values["jia_user_id"]
	if !ok {
		return "", http.StatusUnauthorized, fmt.Errorf("no session")
	}

	jiaUserID := _jiaUserID.(string)
	var count int

	err = db.Get(&count, "SELECT 1 FROM `user` WHERE `jia_user_id` = ? LIMIT 1",
		jiaUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", http.StatusUnauthorized, fmt.Errorf("not found: user")
		}
		return "", http.StatusInternalServerError, fmt.Errorf("db error: %v", err)
	}

	return jiaUserID, 0, nil
}

func getJIAServiceURL(tx *sqlx.Tx) string {
	var config Config
	err := tx.Get(&config, "SELECT * FROM `isu_association_config` WHERE `name` = ?", "jia_service_url")
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Print(err)
		}
		return defaultJIAServiceURL
	}
	return config.URL
}

// POST /initialize
// ????????????????????????
func postInitialize(c echo.Context) error {
	var request InitializeRequest
	err := c.Bind(&request)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	}

	cmd := exec.Command("../sql/init.sh")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	err = cmd.Run()
	if err != nil {
		c.Logger().Errorf("exec init.sh error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	_, err = db.Exec(
		"INSERT INTO `isu_association_config` (`name`, `url`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `url` = VALUES(`url`)",
		"jia_service_url",
		request.JIAServiceURL,
	)
	if err != nil {
		c.Logger().Errorf("db error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	latestIsuConditions := []IsuCondition{}
	if err := db.Select(&latestIsuConditions, "select * from isu_condition a JOIN (select jia_isu_uuid, MAX(`timestamp`) AS `timestamp` FROM isu_condition GROUP BY jia_isu_uuid) b ON a.jia_isu_uuid = b.jia_isu_uuid WHERE a.timestamp = b.timestamp"); err != nil {
		c.Logger().Errorf("db error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	args := make([]interface{}, 0, len(latestIsuConditions)*6)
	placeHolders := &strings.Builder{}
	for i, v := range latestIsuConditions {
		args = append(args, v.JIAIsuUUID, v.Timestamp, v.Level, v.IsSitting, v.Message, v.Condition)
		if i == 0 {
			placeHolders.WriteString(" (?, ?, ?, ?, ?, ?)")
		} else {
			placeHolders.WriteString(",(?, ?, ?, ?, ?, ?)")
		}
	}
	if _, err = db.Exec("INSERT INTO latest_isu_condition (`jia_isu_uuid`, `timestamp`,`level`, `is_sitting`, `message`, `condition`) VALUES"+placeHolders.String(), args...); err != nil {
		c.Logger().Errorf("db error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	//
	//db.Exec("DROP TRIGGER tr1")
	//if _, err := db.Exec("CREATE TRIGGER tr1 BEFORE INSERT ON isu_condition FOR EACH ROW INSERT INTO `latest_isu_level` VALUES (NEW.jia_isu_uuid, NEW.level) ON DUPLICATE KEY UPDATE latest_isu_level.level = NEW.level"); err != nil {
	//	c.Logger().Errorf("db error : %v", err)
	//	return c.NoContent(http.StatusInternalServerError)
	//}

	return c.JSON(http.StatusOK, InitializeResponse{
		Language: "go",
	})
}

// POST /api/auth
// ????????????????????????????????????
func postAuthentication(c echo.Context) error {
	reqJwt := strings.TrimPrefix(c.Request().Header.Get("Authorization"), "Bearer ")

	token, err := jwt.Parse(reqJwt, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, jwt.NewValidationError(fmt.Sprintf("unexpected signing method: %v", token.Header["alg"]), jwt.ValidationErrorSignatureInvalid)
		}
		return jiaJWTSigningKey, nil
	})
	if err != nil {
		switch err.(type) {
		case *jwt.ValidationError:
			return c.String(http.StatusForbidden, "forbidden")
		default:
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.Logger().Errorf("invalid JWT payload")
		return c.NoContent(http.StatusInternalServerError)
	}
	jiaUserIDVar, ok := claims["jia_user_id"]
	if !ok {
		return c.String(http.StatusBadRequest, "invalid JWT payload")
	}
	jiaUserID, ok := jiaUserIDVar.(string)
	if !ok {
		return c.String(http.StatusBadRequest, "invalid JWT payload")
	}

	_, err = db.Exec("INSERT IGNORE INTO user (`jia_user_id`) VALUES (?)", jiaUserID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	session, err := getSession(c.Request())
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	session.Values["jia_user_id"] = jiaUserID
	err = session.Save(c.Request(), c.Response())
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.NoContent(http.StatusOK)
}

// POST /api/signout
// ??????????????????
func postSignout(c echo.Context) error {
	_, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	session, err := getSession(c.Request())
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	session.Options = &sessions.Options{MaxAge: -1, Path: "/"}
	err = session.Save(c.Request(), c.Response())
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.NoContent(http.StatusOK)
}

// GET /api/user/me
// ?????????????????????????????????????????????????????????
func getMe(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	res := GetMeResponse{JIAUserID: jiaUserID}
	return c.JSON(http.StatusOK, res)
}

// GET /api/isu
// ISU??????????????????
func getIsuList(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	type isuListT struct {
		ID         int       `db:"id" json:"id"`
		JIAIsuUUID string    `db:"jia_isu_uuid" json:"jia_isu_uuid"`
		Name       string    `db:"name" json:"name"`
		Image      []byte    `db:"image" json:"-"`
		Character  string    `db:"character" json:"character"`
		JIAUserID  string    `db:"jia_user_id" json:"-"`
		CreatedAt  time.Time `db:"created_at" json:"-"`
		UpdatedAt  time.Time `db:"updated_at" json:"-"`

		Timestamp sql.NullTime   `db:"timestamp"`
		IsSitting sql.NullBool   `db:"is_sitting"`
		Condition sql.NullString `db:"condition"`
		Message   sql.NullString `db:"message"`
		Level     sql.NullString `db:"level"`
	}

	isuList := []isuListT{}

	err = db.Select(
		&isuList,
		"SELECT a.*, b.timestamp, b.is_sitting, b.`condition`, b.message, b.level FROM `isu` a LEFT JOIN `latest_isu_condition` b ON a.jia_isu_uuid = b.jia_isu_uuid WHERE a.`jia_user_id` = ? ORDER BY a.`id` DESC",
		jiaUserID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	responseList := []GetIsuListResponse{}
	for _, isu := range isuList {
		foundLastCondition := isu.Timestamp.Valid

		var formattedCondition *GetIsuConditionResponse
		if foundLastCondition {

			formattedCondition = &GetIsuConditionResponse{
				JIAIsuUUID:     isu.JIAIsuUUID,
				IsuName:        isu.Name,
				Timestamp:      isu.Timestamp.Time.Unix(),
				IsSitting:      isu.IsSitting.Bool,
				Condition:      isu.Condition.String,
				ConditionLevel: isu.Level.String,
				Message:        isu.Message.String,
			}
		}

		res := GetIsuListResponse{
			ID:                 isu.ID,
			JIAIsuUUID:         isu.JIAIsuUUID,
			Name:               isu.Name,
			Character:          isu.Character,
			LatestIsuCondition: formattedCondition}
		responseList = append(responseList, res)
	}

	return c.JSON(http.StatusOK, responseList)
}

// POST /api/isu
// ISU?????????
func postIsu(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	useDefaultImage := false

	jiaIsuUUID := c.FormValue("jia_isu_uuid")
	isuName := c.FormValue("isu_name")
	fh, err := c.FormFile("image")
	if err != nil {
		if !errors.Is(err, http.ErrMissingFile) {
			return c.String(http.StatusBadRequest, "bad format: icon")
		}
		useDefaultImage = true
	}

	var image []byte

	if useDefaultImage {
		image, err = ioutil.ReadFile(defaultIconFilePath)
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	} else {
		file, err := fh.Open()
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
		defer file.Close()

		image, err = ioutil.ReadAll(file)
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}

	tx, err := db.Beginx()
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer tx.Rollback()

	_, err = tx.Exec("INSERT INTO `isu`"+
		"	(`jia_isu_uuid`, `name`, `image`, `jia_user_id`) VALUES (?, ?, ?, ?)",
		jiaIsuUUID, isuName, image, jiaUserID)
	if err != nil {
		mysqlErr, ok := err.(*mysql.MySQLError)

		if ok && mysqlErr.Number == uint16(mysqlErrNumDuplicateEntry) {
			return c.String(http.StatusConflict, "duplicated: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	targetURL := getJIAServiceURL(tx) + "/api/activate"
	body := JIAServiceRequest{postIsuConditionTargetBaseURL, jiaIsuUUID}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	reqJIA, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewBuffer(bodyJSON))
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	reqJIA.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(reqJIA)
	if err != nil {
		c.Logger().Errorf("failed to request to JIAService: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer res.Body.Close()

	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	if res.StatusCode != http.StatusAccepted {
		c.Logger().Errorf("JIAService returned error: status code %v, message: %v", res.StatusCode, string(resBody))
		return c.String(res.StatusCode, "JIAService returned error")
	}

	var isuFromJIA IsuFromJIA
	err = json.Unmarshal(resBody, &isuFromJIA)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	_, err = tx.Exec("UPDATE `isu` SET `character` = ? WHERE  `jia_isu_uuid` = ?", isuFromJIA.Character, jiaIsuUUID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	var isu Isu
	err = tx.Get(
		&isu,
		"SELECT * FROM `isu` WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	omIsu.Set(&isu)
	omIsu2.Set(&isu)

	err = tx.Commit()
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusCreated, isu)
}

// GET /api/isu/:jia_isu_uuid
// ISU??????????????????
func getIsuID(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")

	var res Isu
	err = db.Get(&res, "SELECT * FROM `isu` WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.String(http.StatusNotFound, "not found: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, res)
}

// GET /api/isu/:jia_isu_uuid/icon
// ISU????????????????????????
func getIsuIcon(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")

	var image []byte
	err = db.Get(&image, "SELECT `image` FROM `isu` WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.String(http.StatusNotFound, "not found: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.Blob(http.StatusOK, "", image)
}

// GET /api/isu/:jia_isu_uuid/graph
// ISU??????????????????????????????????????????????????????????????????
func getIsuGraph(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	datetimeStr := c.QueryParam("datetime")
	if datetimeStr == "" {
		return c.String(http.StatusBadRequest, "missing: datetime")
	}
	datetimeInt64, err := strconv.ParseInt(datetimeStr, 10, 64)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad format: datetime")
	}
	date := time.Unix(datetimeInt64, 0).Truncate(time.Hour)

	_, ok := omIsu.Get(jiaIsuUUID, jiaUserID)
	if !ok {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	res, err := generateIsuGraphResponse(db2, jiaIsuUUID, date)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, res)
}

// ??????????????????????????????????????????
func generateIsuGraphResponse(tx *sqlx.DB, jiaIsuUUID string, graphDate time.Time) ([]GraphResponse, error) {
	dataPoints := []GraphDataPointWithInfo{}
	conditionsInThisHour := []IsuCondition{}
	timestampsInThisHour := []int64{}
	var startTimeInThisHour time.Time
	var condition IsuCondition

	rows, err := tx.Queryx("SELECT * FROM `isu_condition` WHERE `jia_isu_uuid` = ? AND `timestamp` BETWEEN ? AND ? ORDER BY `timestamp` ASC", jiaIsuUUID, graphDate, graphDate.Add(time.Hour*24))
	if err != nil {
		return nil, fmt.Errorf("db error: %v", err)
	}

	for rows.Next() {
		err = rows.StructScan(&condition)
		if err != nil {
			return nil, err
		}

		truncatedConditionTime := condition.Timestamp.Truncate(time.Hour)
		if truncatedConditionTime != startTimeInThisHour {
			if len(conditionsInThisHour) > 0 {
				data, err := calculateGraphDataPoint(conditionsInThisHour)
				if err != nil {
					return nil, err
				}

				dataPoints = append(dataPoints,
					GraphDataPointWithInfo{
						JIAIsuUUID:          jiaIsuUUID,
						StartAt:             startTimeInThisHour,
						Data:                data,
						ConditionTimestamps: timestampsInThisHour})
			}

			startTimeInThisHour = truncatedConditionTime
			conditionsInThisHour = []IsuCondition{}
			timestampsInThisHour = []int64{}
		}
		conditionsInThisHour = append(conditionsInThisHour, condition)
		timestampsInThisHour = append(timestampsInThisHour, condition.Timestamp.Unix())
	}

	if len(conditionsInThisHour) > 0 {
		data, err := calculateGraphDataPoint(conditionsInThisHour)
		if err != nil {
			return nil, err
		}

		dataPoints = append(dataPoints,
			GraphDataPointWithInfo{
				JIAIsuUUID:          jiaIsuUUID,
				StartAt:             startTimeInThisHour,
				Data:                data,
				ConditionTimestamps: timestampsInThisHour})
	}

	endTime := graphDate.Add(time.Hour * 24)
	startIndex := len(dataPoints)
	endNextIndex := len(dataPoints)
	for i, graph := range dataPoints {
		if startIndex == len(dataPoints) && !graph.StartAt.Before(graphDate) {
			startIndex = i
		}
		if endNextIndex == len(dataPoints) && graph.StartAt.After(endTime) {
			endNextIndex = i
		}
	}

	filteredDataPoints := []GraphDataPointWithInfo{}
	if startIndex < endNextIndex {
		filteredDataPoints = dataPoints[startIndex:endNextIndex]
	}

	responseList := []GraphResponse{}
	index := 0
	thisTime := graphDate

	for thisTime.Before(graphDate.Add(time.Hour * 24)) {
		var data *GraphDataPoint
		timestamps := []int64{}

		if index < len(filteredDataPoints) {
			dataWithInfo := filteredDataPoints[index]

			if dataWithInfo.StartAt.Equal(thisTime) {
				data = &dataWithInfo.Data
				timestamps = dataWithInfo.ConditionTimestamps
				index++
			}
		}

		resp := GraphResponse{
			StartAt:             thisTime.Unix(),
			EndAt:               thisTime.Add(time.Hour).Unix(),
			Data:                data,
			ConditionTimestamps: timestamps,
		}
		responseList = append(responseList, resp)

		thisTime = thisTime.Add(time.Hour)
	}

	return responseList, nil
}

// ?????????ISU????????????????????????????????????????????????????????????????????????
func calculateGraphDataPoint(isuConditions []IsuCondition) (GraphDataPoint, error) {
	conditionsCount := map[string]int{"is_broken": 0, "is_dirty": 0, "is_overweight": 0}
	rawScore := 0
	for _, condition := range isuConditions {
		badConditionsCount := 0

		if !isValidConditionFormat(condition.Condition) {
			return GraphDataPoint{}, fmt.Errorf("invalid condition format")
		}

		for _, condStr := range strings.Split(condition.Condition, ",") {
			keyValue := strings.Split(condStr, "=")

			conditionName := keyValue[0]
			if keyValue[1] == "true" {
				conditionsCount[conditionName] += 1
				badConditionsCount++
			}
		}

		if badConditionsCount >= 3 {
			rawScore += scoreConditionLevelCritical
		} else if badConditionsCount >= 1 {
			rawScore += scoreConditionLevelWarning
		} else {
			rawScore += scoreConditionLevelInfo
		}
	}

	sittingCount := 0
	for _, condition := range isuConditions {
		if condition.IsSitting {
			sittingCount++
		}
	}

	isuConditionsLength := len(isuConditions)

	score := rawScore * 100 / 3 / isuConditionsLength

	sittingPercentage := sittingCount * 100 / isuConditionsLength
	isBrokenPercentage := conditionsCount["is_broken"] * 100 / isuConditionsLength
	isOverweightPercentage := conditionsCount["is_overweight"] * 100 / isuConditionsLength
	isDirtyPercentage := conditionsCount["is_dirty"] * 100 / isuConditionsLength

	dataPoint := GraphDataPoint{
		Score: score,
		Percentage: ConditionsPercentage{
			Sitting:      sittingPercentage,
			IsBroken:     isBrokenPercentage,
			IsOverweight: isOverweightPercentage,
			IsDirty:      isDirtyPercentage,
		},
	}
	return dataPoint, nil
}

// GET /api/condition/:jia_isu_uuid
// ISU?????????????????????????????????
func getIsuConditions(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	if jiaIsuUUID == "" {
		return c.String(http.StatusBadRequest, "missing: jia_isu_uuid")
	}

	endTimeInt64, err := strconv.ParseInt(c.QueryParam("end_time"), 10, 64)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad format: end_time")
	}
	endTime := time.Unix(endTimeInt64, 0)
	conditionLevelCSV := c.QueryParam("condition_level")
	if conditionLevelCSV == "" {
		return c.String(http.StatusBadRequest, "missing: condition_level")
	}
	conditionLevel := map[string]interface{}{}
	for _, level := range strings.Split(conditionLevelCSV, ",") {
		conditionLevel[level] = struct{}{}
	}

	startTimeStr := c.QueryParam("start_time")
	var startTime time.Time
	if startTimeStr != "" {
		startTimeInt64, err := strconv.ParseInt(startTimeStr, 10, 64)
		if err != nil {
			return c.String(http.StatusBadRequest, "bad format: start_time")
		}
		startTime = time.Unix(startTimeInt64, 0)
	}

	isu, ok := omIsu.Get(jiaIsuUUID, jiaUserID)
	if !ok {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	isuName := isu.Name

	conditionsResponse, err := getIsuConditionsFromDB(db, jiaIsuUUID, endTime, conditionLevel, startTime, conditionLimit, isuName)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	return c.JSON(http.StatusOK, conditionsResponse)
}

// ISU???????????????????????????DB????????????
func getIsuConditionsFromDB(db *sqlx.DB, jiaIsuUUID string, endTime time.Time, conditionLevel map[string]interface{}, startTime time.Time,
	limit int, isuName string) ([]*GetIsuConditionResponse, error) {

	conditions := []IsuCondition{}
	var (
		query  string
		params []interface{}
		err    error
	)

	levels := make([]string, 0, len(conditionLevel))
	for k, _ := range conditionLevel {
		levels = append(levels, k)
	}

	if startTime.IsZero() {
		query, params, err = sqlx.In(
			"SELECT * FROM `isu_condition` WHERE `jia_isu_uuid` = ? AND `timestamp` < ? AND `level` IN (?) ORDER BY `timestamp` DESC LIMIT ?",
			jiaIsuUUID, endTime, levels, limit,
		)
	} else {
		query, params, err = sqlx.In(
			"SELECT * FROM `isu_condition` WHERE `jia_isu_uuid` = ? AND `timestamp` < ? AND ? <= `timestamp` AND `level` IN (?) ORDER BY `timestamp` DESC LIMIT ?",
			jiaIsuUUID, endTime, startTime, levels, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("db error: %v", err)
	}
	if err := db2.Select(&conditions, db.Rebind(query), params...); err != nil {
		return nil, fmt.Errorf("db error: %v", err)
	}

	conditionsResponse := make([]*GetIsuConditionResponse, 0, len(conditions))
	for _, c := range conditions {
		data := GetIsuConditionResponse{
			JIAIsuUUID:     c.JIAIsuUUID,
			IsuName:        isuName,
			Timestamp:      c.Timestamp.Unix(),
			IsSitting:      c.IsSitting,
			Condition:      c.Condition,
			ConditionLevel: c.Level,
			Message:        c.Message,
		}
		conditionsResponse = append(conditionsResponse, &data)
	}

	return conditionsResponse, nil
}

// ISU?????????????????????????????????????????????????????????????????????????????????
func calculateConditionLevel(condition string) (string, error) {
	var conditionLevel string

	warnCount := strings.Count(condition, "=true")
	switch warnCount {
	case 0:
		conditionLevel = conditionLevelInfo
	case 1, 2:
		conditionLevel = conditionLevelWarning
	case 3:
		conditionLevel = conditionLevelCritical
	default:
		return "", fmt.Errorf("unexpected warn count")
	}

	return conditionLevel, nil
}

// GET /api/trend
// ISU???????????????????????????????????????????????????
func getTrend(c echo.Context) error {

	v, err, _ := group.Do("trend", func() (interface{}, error) {
		characterList := []string{
			"???????????????", "???????????????", "???????????????", "????????????", "????????????", "???????????????",
			"???????????????", "????????????", "???????????????", "???????????????", "?????????", "????????????",
			"????????????", "?????????", "????????????", "???????????????", "?????????", "????????????", "?????????",
			"????????????", "????????????", "????????????", "?????????", "????????????", "????????????",
		}
		res := []TrendResponse{}

		var err error
		for _, character := range characterList {
			isuList := []Isu{}
			err = db.Select(&isuList,
				"SELECT a.*, b.level, b.timestamp FROM `isu` a JOIN `latest_isu_condition` b ON a.jia_isu_uuid = b.jia_isu_uuid WHERE a.`character` = ?",
				character,
			)
			if err != nil {
				c.Logger().Errorf("db error: %v", err)
				return nil, err
			}

			characterInfoIsuConditions := []*TrendCondition{}
			characterWarningIsuConditions := []*TrendCondition{}
			characterCriticalIsuConditions := []*TrendCondition{}
			for _, isu := range isuList {
				if isu.Level != "" {
					trendCondition := TrendCondition{
						ID:        isu.ID,
						Timestamp: isu.Timestamp.Unix(),
					}
					switch isu.Level {
					case "info":
						characterInfoIsuConditions = append(characterInfoIsuConditions, &trendCondition)
					case "warning":
						characterWarningIsuConditions = append(characterWarningIsuConditions, &trendCondition)
					case "critical":
						characterCriticalIsuConditions = append(characterCriticalIsuConditions, &trendCondition)
					}
				}

			}

			sort.Slice(characterInfoIsuConditions, func(i, j int) bool {
				return characterInfoIsuConditions[i].Timestamp > characterInfoIsuConditions[j].Timestamp
			})
			sort.Slice(characterWarningIsuConditions, func(i, j int) bool {
				return characterWarningIsuConditions[i].Timestamp > characterWarningIsuConditions[j].Timestamp
			})
			sort.Slice(characterCriticalIsuConditions, func(i, j int) bool {
				return characterCriticalIsuConditions[i].Timestamp > characterCriticalIsuConditions[j].Timestamp
			})
			res = append(res,
				TrendResponse{
					Character: character,
					Info:      characterInfoIsuConditions,
					Warning:   characterWarningIsuConditions,
					Critical:  characterCriticalIsuConditions,
				})
		}
		return res, nil
	})

	if err != nil {
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, v.([]TrendResponse))
}

// POST /api/condition/:jia_isu_uuid
// ISU?????????????????????????????????????????????
func postIsuCondition(c echo.Context) error {
	// TODO: ?????????????????????????????????????????????????????????????????????????????????????????????????????????????????????
	//dropProbability := 0.9
	//if rand.Float64() <= dropProbability {
	//	c.Logger().Warnf("drop post isu condition request")
	//	return c.NoContent(http.StatusAccepted)
	//}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	if jiaIsuUUID == "" {
		return c.String(http.StatusBadRequest, "missing: jia_isu_uuid")
	}

	req := []PostIsuConditionRequest{}
	err := c.Bind(&req)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	} else if len(req) == 0 {
		return c.String(http.StatusBadRequest, "bad request body")
	}

	_, ok := omIsu2.Get(jiaIsuUUID)
	if !ok {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	isuConditions := make([]*IsuCondition, 0, len(req))

	for _, cond := range req {
		timestamp := time.Unix(cond.Timestamp, 0)

		if !isValidConditionFormat(cond.Condition) {
			return c.String(http.StatusBadRequest, "bad request body")
		}

		level, err := calculateConditionLevel(cond.Condition)
		if err != nil {
			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}

		isuConditions = append(isuConditions, &IsuCondition{
			JIAIsuUUID: jiaIsuUUID,
			Timestamp:  timestamp,
			IsSitting:  cond.IsSitting,
			Condition:  cond.Condition,
			Message:    cond.Message,
			Level:      level,
		})
		//
		//_, err = tx.Exec(
		//	"INSERT INTO `isu_condition`"+
		//		"	(`jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message`)"+
		//		"	VALUES (?, ?, ?, ?, ?)",
		//	jiaIsuUUID, timestamp, cond.IsSitting, cond.Condition, cond.Message)
		//if err != nil {
		//	c.Logger().Errorf("db error: %v", err)
		//	return c.NoContent(http.StatusInternalServerError)
		//}
	}

	omIsuConditionList.Set(isuConditions)
	//
	//args := make([]interface{}, 0, len(isuConditions)*5)
	//placeHolders := &strings.Builder{}
	//for i, v := range isuConditions {
	//	args = append(args, v.JIAIsuUUID, v.Timestamp, v.IsSitting, v.Condition, v.Message)
	//	if i == 0 {
	//		placeHolders.WriteString(" (?, ?, ?, ?, ?)")
	//	} else {
	//		placeHolders.WriteString(",(?, ?, ?, ?, ?)")
	//	}
	//}
	//_, err = db.Exec("INSERT INTO `isu_condition` (`jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message`) VALUES"+placeHolders.String(), args...)
	//if err != nil {
	//	c.Logger().Errorf("db error: %v", err)
	//	return c.NoContent(http.StatusInternalServerError)
	//}

	return c.NoContent(http.StatusAccepted)
}

func loopPostIsuCondition() {
	for range time.Tick(time.Millisecond * 100) {
		isuConditions := omIsuConditionList.Get()
		if len(isuConditions) == 0 {
			continue
		}
		args := make([]interface{}, 0, len(isuConditions)*6)
		placeHolders := &strings.Builder{}
		for i, v := range isuConditions {
			args = append(args, v.JIAIsuUUID, v.Timestamp, v.IsSitting, v.Condition, v.Message, v.Level)
			if i == 0 {
				placeHolders.WriteString(" (?, ?, ?, ?, ?, ?)")
			} else {
				placeHolders.WriteString(",(?, ?, ?, ?, ?, ?)")
			}
		}
		_, err := db2.Exec("INSERT INTO `isu_condition` (`jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message`, `level`) VALUES"+placeHolders.String(), args...)
		if err != nil {
			log.Println(err)
		}

		bulkInsertLatestIsuLevels(isuConditions)
	}
}

func bulkInsertLatestIsuLevels(isuConditions []*IsuCondition) {
	latestIsuConditions := make([]LatestIsuCondition, 0, len(isuConditions))
	for _, v := range isuConditions {
		latestIsuConditions = append(latestIsuConditions, LatestIsuCondition{
			JIAIsuUUID: v.JIAIsuUUID,
			Timestamp:  v.Timestamp,
			IsSitting:  v.IsSitting,
			Condition:  v.Condition,
			Message:    v.Message,
			Level:      v.Level,
		})
	}
	if _, err := db.NamedExec("INSERT INTO `latest_isu_condition` (`jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message`, `level`) VALUES (:jia_isu_uuid, :timestamp, :is_sitting, :condition, :message"+
		", :level) ON DUPLICATE KEY UPDATE `timestamp`=VALUES(`timestamp`), `is_sitting`=VALUES(`is_sitting`), `condition`=VALUES(`condition`), `message`=VALUES(`message`), `level`=VALUES(`level`)", latestIsuConditions); err != nil {
		log.Println(err)
	}
}

// ISU???????????????????????????????????????csv?????????????????????????????????
func isValidConditionFormat(conditionStr string) bool {

	keys := []string{"is_dirty=", "is_overweight=", "is_broken="}
	const valueTrue = "true"
	const valueFalse = "false"

	idxCondStr := 0

	for idxKeys, key := range keys {
		if !strings.HasPrefix(conditionStr[idxCondStr:], key) {
			return false
		}
		idxCondStr += len(key)

		if strings.HasPrefix(conditionStr[idxCondStr:], valueTrue) {
			idxCondStr += len(valueTrue)
		} else if strings.HasPrefix(conditionStr[idxCondStr:], valueFalse) {
			idxCondStr += len(valueFalse)
		} else {
			return false
		}

		if idxKeys < (len(keys) - 1) {
			if conditionStr[idxCondStr] != ',' {
				return false
			}
			idxCondStr++
		}
	}

	return (idxCondStr == len(conditionStr))
}

func getIndex(c echo.Context) error {
	return c.File(frontendContentsPath + "/index.html")
}
