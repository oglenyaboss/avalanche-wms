package main

import (
	"crypto/ecdh"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

// orgFile — формат org.json, общий с WMS (менять нельзя).
type orgFile struct {
	OrgID string `json:"orgId"`
	Name  string `json:"name"`
	QX    string `json:"qx"`
	QY    string `json:"qy"`
}

type orgRequest struct {
	orgID  [16]byte
	name   string
	qx, qy [32]byte
	keyID  common.Hash
}

func parseOrgFile(data []byte) (orgRequest, error) {
	var f orgFile
	if err := json.Unmarshal(data, &f); err != nil {
		return orgRequest{}, fmt.Errorf("org.json: %w", err)
	}
	id, err := uuid.Parse(f.OrgID)
	if err != nil {
		return orgRequest{}, fmt.Errorf("orgId: %w", err)
	}
	name := strings.TrimSpace(f.Name)
	if name == "" {
		return orgRequest{}, errors.New("name: пустое имя организации")
	}
	qx, err := parseCoord("qx", f.QX)
	if err != nil {
		return orgRequest{}, err
	}
	qy, err := parseCoord("qy", f.QY)
	if err != nil {
		return orgRequest{}, err
	}
	if _, err := ecdh.P256().NewPublicKey(append(append([]byte{4}, qx[:]...), qy[:]...)); err != nil {
		return orgRequest{}, errors.New("публичный ключ не на кривой P-256")
	}
	return orgRequest{
		orgID: [16]byte(id),
		name:  name,
		qx:    qx,
		qy:    qy,
		keyID: crypto.Keccak256Hash(qx[:], qy[:]),
	}, nil
}

func parseCoord(field, s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 66 || !strings.HasPrefix(s, "0x") {
		return out, fmt.Errorf("%s: ожидается 0x и 64 hex-символа", field)
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return out, fmt.Errorf("%s: %w", field, err)
	}
	copy(out[:], b)
	return out, nil
}
