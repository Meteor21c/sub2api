package service

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const fzyAssetRequestIdentityKey = "meteor.fzy_asset_request_identity"

const (
	fzyAssetOwnerHeader       = "X-Meteor-Fzy-Owner"
	fzyAssetReservationHeader = "X-Meteor-Fzy-Reservation"
)

type fzyAssetRequestIdentity struct {
	userID        int64
	reservationID string
}

// SetFZYAssetOwner records the authenticated downstream user, never a client
// header. A typed value keeps the internal bridge identity out of request data.
func SetFZYAssetOwner(c *gin.Context, userID int64) {
	if c != nil {
		c.Set(fzyAssetRequestIdentityKey, fzyAssetRequestIdentity{userID: userID})
	}
}

// SetFZYAssetReservation binds queue idempotency to an existing reservation.
// It does not create or modify a reservation, or change its billing rules.
func SetFZYAssetReservation(c *gin.Context, reservationID string) {
	if c == nil {
		return
	}
	identity := fzyAssetIdentityFromContext(c)
	reservationID = strings.TrimSpace(reservationID)
	if len(reservationID) <= 128 && !strings.ContainsAny(reservationID, "\r\n\x00") {
		identity.reservationID = reservationID
	} else {
		identity.reservationID = ""
	}
	c.Set(fzyAssetRequestIdentityKey, identity)
}

func fzyAssetIdentityFromContext(c *gin.Context) fzyAssetRequestIdentity {
	if c == nil {
		return fzyAssetRequestIdentity{}
	}
	value, _ := c.Get(fzyAssetRequestIdentityKey)
	identity, _ := value.(fzyAssetRequestIdentity)
	return identity
}

func applyFZYAssetRequestHeaders(header http.Header, account *Account, identity fzyAssetRequestIdentity) {
	// These are reserved internal headers. Strip account overrides too, so a
	// missing identity cannot silently turn into a different user's identity.
	for name := range header {
		if strings.EqualFold(name, fzyAssetOwnerHeader) || strings.EqualFold(name, fzyAssetReservationHeader) {
			delete(header, name)
		}
	}
	if !IsFZYVideoBridgeAccount(account) || identity.userID <= 0 {
		return
	}
	header.Set(fzyAssetOwnerHeader, "user-"+strconv.FormatInt(identity.userID, 10))
	if identity.reservationID != "" {
		header.Set(fzyAssetReservationHeader, identity.reservationID)
	}
}

func applyFZYAssetContextHeaders(header http.Header, c *gin.Context, account *Account) {
	applyFZYAssetRequestHeaders(header, account, fzyAssetIdentityFromContext(c))
}
