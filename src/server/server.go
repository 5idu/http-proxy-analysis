package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hellojqk/http-proxy-analysis/src/entity"
	"github.com/hellojqk/http-proxy-analysis/src/repository"
	"github.com/hellojqk/http-proxy-analysis/src/service"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (r responseBodyWriter) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r responseBodyWriter) WriteString(s string) (n int, err error) {
	r.body.WriteString(s)
	return r.ResponseWriter.WriteString(s)
}

// Run .
func Run(appName string) {
	repository.InitConn()
	var err error

	application, err = service.GetAPP(appName)
	if err != nil || application == nil {
		panic(err)
	}

	go func() {
		for {
			ReloadAPIInfo(appName)
			time.Sleep(time.Second * 10)
		}
	}()

	g := gin.Default()
	g.Use(logResponseBody())
	proxyHost, _ := url.Parse(application.ProxyHost)
	proxy := httputil.NewSingleHostReverseProxy(proxyHost)
	// proxy.Transport = &http.Transport{
	// 	Proxy: func(*http.Request) (*url.URL, error) {
	// 		return url.Parse("http://127.0.0.1:8899")
	// 	},
	// 	TLSClientConfig: &tls.Config{
	// 		InsecureSkipVerify: true, // 忽略证书验证
	// 	},
	// }
	host := proxyHost.Host

	var handlerFunc = func(c *gin.Context) {
		c.Request.Header.Set("Host", host)
		c.Request.Host = host
		proxy.ServeHTTP(c.Writer, c.Request)
	}
	g.Any("/*action", handlerFunc)

	g.Run(fmt.Sprintf(":%d", viper.GetInt("serverPort")))
}

// parseAry 将url转换成数组 /a/b/c ["a","b","c"]
func parseAry(url string) []string {
	lowURLAry := strings.Split(strings.ToLower(url), "/")
	lowURLTempAry := make([]string, 0, len(lowURLAry))
	for _, v := range lowURLAry {
		if v != "" {
			lowURLTempAry = append(lowURLTempAry, v)
		}
	}
	return lowURLTempAry
}

// getURLInfo
func getURLInfo(app *entity.Application) (map[string]*entity.API, map[string][]string) {
	var apiInfoMap = make(map[string]*entity.API, len(app.APIs))
	var restfulURLMap = make(map[string][]string, len(app.APIs))
	for _, api := range app.APIs {
		lowURL := strings.ToLower(api.URL)
		apiInfoMap[lowURL] = api
		restfulURLMap[lowURL] = parseAry(lowURL)
	}
	return apiInfoMap, restfulURLMap
}

// matchURL 匹配URL对应的api id
func matchURL(apiInfoMap map[string]*entity.API, restfulURLMap map[string][]string, requestURL string) *entity.API {
	url, err := url.ParseRequestURI(requestURL)
	if err != nil {
		log.Err(err).Msg("url.ParseRequestURI")
	}

	lowPath := strings.ToLower(url.Path)
	var restfulURL string
	_, ok := restfulURLMap[lowPath]
	//如果能直接匹配路由则直接使用
	if ok {
		restfulURL = lowPath
	} else {
		tempPathAry := parseAry(lowPath)

		for key, restfulAry := range restfulURLMap {
			//如果当前路由的长度和配置长度不一致则跳过
			if len(restfulAry) != len(tempPathAry) {
				continue
			}
			match := true
			// tempPathAry /a/b/123/c
			// restfulAry /a/b/{id}/c
			for index, tempItem := range tempPathAry {
				if !match {
					break
				}

				//直接匹配当前项
				if restfulAry[index] == tempItem {
					continue
				}

				//如果最后一位是{证明也能匹配
				if restfulAry[index][0] == '{' {
					if index == len(tempPathAry)-1 || restfulAry[index+1] == tempPathAry[index+1] {
						continue
					}
				}
				match = false
			}
			if match {
				restfulURL = key
			}
		}
	}
	if restfulURL == "" {
		return nil
	}
	return apiInfoMap[restfulURL]
}

// ErrorDontRedirect 自定义不要重定向错误
var ErrorDontRedirect = errors.New("don't redirect")

var cli = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return ErrorDontRedirect
	},
}

var application *entity.Application
var apiInfoMap map[string]*entity.API
var restfulURLMap map[string][]string

// ReloadAPIInfo 重新加载API信息
func ReloadAPIInfo(appName string) {
	app, err := service.GetAPP(appName)
	if err != nil || app == nil {
		return
	}
	application = app
	apiInfoMap, restfulURLMap = getURLInfo(app)
}

// logResponseBody 记录应用访问信息
func logResponseBody() gin.HandlerFunc {

	return func(c *gin.Context) {
		//swagger文档地址则跳过处理
		if strings.HasPrefix(c.Request.RequestURI, "/swagger") {
			c.Next()
			return
		}
		apiInfo := matchURL(apiInfoMap, restfulURLMap, c.Request.RequestURI)
		w := &responseBodyWriter{body: &bytes.Buffer{}, ResponseWriter: c.Writer}
		c.Writer = w

		proxyLog := &entity.ProxyLog{
			ApplicationID:      application.ID,
			ProxyRequestMethod: c.Request.Method,
			Status:             true,
		}
		if apiInfo != nil {
			//如果是配置的API，那么没有支持的接口不进行代理日志处理，这样是为了屏蔽部分接口大量调用导致数据过多
			switch c.Request.Method {
			case "GET":
				if !apiInfo.GET {
					c.Next()
					return
				}
			case "POST":
				if !apiInfo.POST {
					c.Next()
					return
				}
			case "PUT":
				if !apiInfo.PUT {
					c.Next()
					return
				}
			case "PATCH":
				if !apiInfo.PATCH {
					c.Next()
					return
				}
			case "DELETE":
				if !apiInfo.DELETE {
					c.Next()
					return
				}
			}
			proxyLog.APIID = apiInfo.ID
		}

		//记录请求信息参数
		requestData, err := c.GetRawData()
		if err != nil {
			log.Err(err).Msg("c.GetRawData()")
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestData))

		proxyLog.ProxyRequestBody = string(requestData)
		proxyLog.ProxyRequestURL = c.Request.RequestURI

		requestHeaderBts, err := json.Marshal(c.Request.Header)
		if err != nil {
			log.Err(err).Msg("json.Marshal(c.Request.Header)")
		}
		proxyLog.ProxyRequestHeader = string(requestHeaderBts)

		oldBeginTime := time.Now().UnixNano()
		c.Next()
		proxyLog.ProxyResponseStatus = c.Writer.Status()
		responseHeaderBts, err := json.Marshal(c.Writer.Header())
		if err != nil {
			log.Err(err).Msg("json.Marshal(c.Writer.Header())")
		}
		proxyLog.ProxyResponseHeader = string(responseHeaderBts)

		// 判断返回信息是否压缩
		contentEncoding := c.Writer.Header().Get("Content-Encoding")
		switch strings.ToLower(contentEncoding) {
		case "gzip":
			reader, _ := gzip.NewReader(w.body)
			if reader != nil {
				readerBts, _ := io.ReadAll(reader)
				proxyLog.ProxyResponseBody = string(readerBts)
			}
		// todo 支持其他压缩算法
		default:
			proxyLog.ProxyResponseBody = w.body.String()
		}
		proxyLog.ProxyDuration = (time.Now().UnixNano() - oldBeginTime) / 1e6

		//配置了新的站点，接口不在配置列表里默认允许get镜像，接口在配置列表里则按照配置是否允许镜像
		// 为了避免镜像服务异常或响应过慢影响本地请求，
		// 将镜像请求和日志入库放到独立 goroutine 中异步执行。
		needMirror := application.ImageHost != "" && ((apiInfo == nil && c.Request.Method == "GET") ||
			(apiInfo != nil &&
				((c.Request.Method == "GET" && apiInfo.GETAllowMirror) ||
					(c.Request.Method == "POST" && apiInfo.POSTAllowMirror) ||
					(c.Request.Method == "PUT" && apiInfo.PUTAllowMirror) ||
					(c.Request.Method == "PATCH" && apiInfo.PATCHAllowMirror) ||
					(c.Request.Method == "DELETE" && apiInfo.DELETEAllowMirror))))

		// 由于 gin.Context 不能在 goroutine 中使用，这里只拷贝必要的数据
		requestMethod := c.Request.Method
		requestURI := c.Request.RequestURI
		requestHeader := c.Request.Header.Clone()
		requestBodyCopy := make([]byte, len(requestData))
		copy(requestBodyCopy, requestData)

		go func(pl *entity.ProxyLog) {
			if needMirror {
				imageRequest, err := http.NewRequest(requestMethod, application.ImageHost+requestURI, bytes.NewReader(requestBodyCopy))
				if err != nil {
					log.Err(err).Msg("http.ImageRequest")
				} else {
					imageRequest.Header = requestHeader
					newBeginTime := time.Now().UnixNano()
					// 发送镜像请求
					imageResponse, err := cli.Do(imageRequest)
					if err != nil && err != ErrorDontRedirect {
						log.Err(err).Msg("cli.Do(imageRequest)")
					}
					if imageResponse != nil {
						defer imageResponse.Body.Close()
						pl.ImageDuration = (time.Now().UnixNano() - newBeginTime) / 1e6
						pl.ImageResponseStatus = imageResponse.StatusCode
						imageResponseHeaderBts, err := json.Marshal(imageResponse.Header)
						if err != nil {
							log.Err(err).Msg("json.Marshal(imageResponse.Header)")
						}
						pl.ImageResponseHeader = string(imageResponseHeaderBts)

						// 判断返回信息是否压缩
						contentEncoding := imageResponse.Header.Get("Content-Encoding")
						switch strings.ToLower(contentEncoding) {
						case "gzip":
							reader, err := gzip.NewReader(imageResponse.Body)
							if err != nil {
								log.Err(err).Msg("gzip.NewReader(imageResponse.Body)")
								break
							}
							defer reader.Close()
							readerBts, err := io.ReadAll(reader)
							if err != nil {
								log.Err(err).Msg("io.ReadAll(reader)")
							}
							pl.ImageResponseBody = string(readerBts)
						// todo 支持其他压缩算法
						default:
							readerBts, err := io.ReadAll(imageResponse.Body)
							if err != nil {
								log.Err(err).Msg("io.ReadAll(imageResponse.Body)")
							}
							pl.ImageResponseBody = string(readerBts)
						}
					}
				}
			}

			result, err := service.InsertProwyLog(pl)
			if !result {
				log.Err(err).Msg("WriteLog Failed")
			}
		}(proxyLog)
	}
}
