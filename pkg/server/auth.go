package server

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type loginClaim struct {
	jwt.RegisteredClaims
	UserID    any    `json:"userId"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	HomeOrg   string `json:"homeOrg"`
	Groups    string `json:"groups"`
}

type User struct {
	UserID    string   `json:"userId"`
	Email     string   `json:"email"`
	FirstName string   `json:"firstName"`
	LastName  string   `json:"lastName"`
	HomeOrg   string   `json:"homeOrg"`
	Groups    []string `json:"groups"`
}

func (user *User) IsLoggedIn() bool {
	return !(len(user.Groups) == 0 || (len(user.Groups) == 1 && user.Groups[0] == "global/guest"))
}

func (ctrl *Controller) AuthHandler(ctx *gin.Context) {
	hasCookie := false
	bearerToken := ctx.Request.Header.Get("Authorization")
	if bearerToken == "" {
		bearerToken = ctx.Request.URL.Query().Get("token")
	} else {
		if bearerToken[:7] != "Bearer " {
			ctx.Next()
			return
		}
		bearerToken = bearerToken[7:]
	}
	if bearerToken == "" {
		if cookie, err := ctx.Cookie("token"); err == nil {
			bearerToken = cookie
			hasCookie = true
		}
	}
	if bearerToken == "" {
		ctx.Next()
		return
	}

	claim := &loginClaim{}
	token, err := jwt.ParseWithClaims(bearerToken, claim, func(token *jwt.Token) (interface{}, error) {
		talg := token.Method.Alg()
		algOK := false
		for _, a := range ctrl.loginJWTAlgs {
			if talg == a {
				algOK = true
				break
			}
		}
		if !algOK {
			ctx.SetCookie("token", "", -1, "/", "", false, false)
			return false, fmt.Errorf("unexpected signing method (allowed are %v): %v", ctrl.loginJWTAlgs, token.Header["alg"])
		}
		return []byte(ctrl.loginJWTKey), nil
	})
	if err != nil {
		ctx.SetCookie("token", "", -1, "/", "", false, false)
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, fmt.Sprintf("cannot parse token: %v", err))
		return
	}
	if !token.Valid {
		// remove cookie
		ctx.SetCookie("token", "", -1, "/", "", false, false)
		//		ctx.AbortWithStatusJSON(http.StatusUnauthorized, "invalid token")
		ctx.Next()
		return
	}
	if !slices.Contains([]string{ctrl.loginIssuer, "revcatfront"}, claim.Issuer) {
		ctx.SetCookie("token", "", -1, "/", "", false, false)
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, fmt.Sprintf("invalid issuer: %s", claim.Issuer))
		return
	}
	user := &User{
		UserID:    fmt.Sprintf("%v", claim.UserID),
		Email:     claim.Email,
		FirstName: claim.FirstName,
		LastName:  claim.LastName,
		HomeOrg:   claim.HomeOrg,
		Groups:    ctrl.locationGroups(ctx),
	}
	if claim.Groups != "" {
		user.Groups = append(user.Groups, strings.Split(claim.Groups, ";")...)
	}
	ctx.Set("user", user)
	if !hasCookie {
		claim.Issuer = "revcatfront"
		claim.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Hour * 8))
		claim.IssuedAt = jwt.NewNumericDate(time.Now())
		if newTokenString, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claim).SignedString([]byte(ctrl.loginJWTKey)); err == nil {
			ctx.SetCookie("token", newTokenString, 60*23, "/", "", false, false)
		}
	}
	ctx.Next()
}

func GetUser(ctx *gin.Context) *User {
	userAny, ok := ctx.Get("user")
	if !ok {
		return &User{
			Groups: []string{"global/guest"},
		}
	}
	user, ok := userAny.(*User)
	if !ok {
		return &User{
			Groups: []string{"global/guest"},
		}
	}
	return user
}

func (ctrl *Controller) locationGroups(ctx *gin.Context) []string {
	ip := net.ParseIP(ctx.ClientIP())
	if ip == nil {
		return []string{}
	}
	groups := []string{}
	for location, nets := range ctrl.locations {
		for _, n := range nets {
			if n.Contains(ip) {
				groups = append(groups, location)
				break
			}
		}
	}
	return groups
}

func NewJWT(secret string, subject string, alg string, valid int64, domain string, issuer string, userId string) (tokenString string, err error) {

	var signingMethod jwt.SigningMethod
	switch strings.ToLower(alg) {
	case "hs256":
		signingMethod = jwt.SigningMethodHS256
	case "hs384":
		signingMethod = jwt.SigningMethodHS384
	case "hs512":
		signingMethod = jwt.SigningMethodHS512
	case "es256":
		signingMethod = jwt.SigningMethodES256
	case "es384":
		signingMethod = jwt.SigningMethodES384
	case "es512":
		signingMethod = jwt.SigningMethodES512
	case "ps256":
		signingMethod = jwt.SigningMethodPS256
	case "ps384":
		signingMethod = jwt.SigningMethodPS384
	case "ps512":
		signingMethod = jwt.SigningMethodPS512
	default:
		return "", errors.Wrapf(err, "invalid signing method %s", alg)
	}
	exp := time.Now().Unix() + valid
	claims := jwt.MapClaims{
		"sub": strings.ToLower(subject),
		"exp": exp,
	}
	// keep jwt short, no empty Fields
	if domain != "" {
		claims["aud"] = domain
	}
	if issuer != "" {
		claims["iss"] = issuer
	}
	if userId != "" {
		claims["user"] = userId
	}

	token := jwt.NewWithClaims(signingMethod, claims)
	//	log.Println("NewJWT( ", secret, ", ", subject, ", ", exp)
	tokenString, err = token.SignedString([]byte(secret))
	return tokenString, err
}
