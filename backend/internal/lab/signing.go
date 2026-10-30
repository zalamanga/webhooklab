package lab

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

func Signature(master []byte, destination, eventID, timestamp string, body []byte) string {
	derive := hmac.New(sha256.New, master)
	derive.Write([]byte(destination))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("." + eventID + "."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func VerifySignature(master []byte, destination, eventID, timestamp, signature string, body []byte, now time.Time) bool {
	if !ValidID(eventID) || len(timestamp) == 0 || len(timestamp) > 12 {
		return false
	}
	for _, digit := range timestamp {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	difference := now.Unix() - seconds
	if difference < -300 || difference > 300 {
		return false
	}
	if !strings.HasPrefix(signature, "v1=") || len(signature) != 67 {
		return false
	}
	supplied, err := hex.DecodeString(signature[3:])
	if err != nil {
		return false
	}
	expected, _ := hex.DecodeString(Signature(master, destination, eventID, timestamp, body)[3:])
	return hmac.Equal(supplied, expected)
}
