// registry-cli — CLI владельца консорциума: регистрация организаций в
// ConsortiumRegistry и просмотр реестра. Собирается в образ ledger-adapter
// (бинарь /registry-cli), запуск — цель registry-cli в Makefile.
package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

const usage = `usage:
  registry-cli register-org --file <org.json>
  registry-cli add-admin-key --file <org.json>
  registry-cli list

окружение: RPC_URL, REGISTRY_ADDR (или <NAME>_FILE); OWNER_KEY (или OWNER_KEY_FILE) — для register-org и add-admin-key
`

var errUsage = errors.New("usage")

func main() {
	err := run(os.Args[1:])
	if errors.Is(err, errUsage) {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry-cli: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	switch args[0] {
	case "register-org":
		return registerOrg(ctx, args[1:])
	case "add-admin-key":
		return addAdminKey(ctx, args[1:])
	case "list":
		return list(ctx)
	default:
		return errUsage
	}
}

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

func registerOrg(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("register-org", flag.ExitOnError)
	file := fs.String("file", "", "путь к org.json")
	_ = fs.Parse(args)
	if *file == "" {
		return errors.New("register-org: нужен --file")
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	org, err := parseOrgFile(data)
	if err != nil {
		return err
	}

	ec, addr, err := dialRegistry()
	if err != nil {
		return err
	}
	defer ec.Close()
	reg, err := chain.NewRegistry(ec, addr)
	if err != nil {
		return err
	}

	exists, err := reg.OrganizationExists(ctx, org.orgID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("организация %s уже зарегистрирована", uuid.UUID(org.orgID))
	}
	key, err := reg.GetKey(ctx, [32]byte(org.keyID))
	if err != nil {
		return err
	}
	if key.Role != 0 {
		return fmt.Errorf("ключ %s уже зарегистрирован", org.keyID.Hex())
	}

	owner, err := ownerKey()
	if err != nil {
		return err
	}
	calldata, err := reg.ABI().Pack("registerOrganization", org.orgID, org.name, org.qx, org.qy)
	if err != nil {
		return err
	}
	hash, err := sendOwnerTx(ctx, ec, owner, addr, calldata)
	if err != nil {
		return err
	}
	fmt.Printf("организация %s «%s» зарегистрирована, ключ админа %s, tx %s\n",
		uuid.UUID(org.orgID), org.name, org.keyID.Hex(), hash.Hex())
	return nil
}

func addAdminKey(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("add-admin-key", flag.ExitOnError)
	file := fs.String("file", "", "путь к org.json")
	_ = fs.Parse(args)
	if *file == "" {
		return errors.New("add-admin-key: нужен --file")
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	org, err := parseOrgFile(data)
	if err != nil {
		return err
	}

	ec, addr, err := dialRegistry()
	if err != nil {
		return err
	}
	defer ec.Close()
	reg, err := chain.NewRegistry(ec, addr)
	if err != nil {
		return err
	}

	exists, err := reg.OrganizationExists(ctx, org.orgID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("организация %s не зарегистрирована", uuid.UUID(org.orgID))
	}
	key, err := reg.GetKey(ctx, [32]byte(org.keyID))
	if err != nil {
		return err
	}
	if key.Role != 0 {
		return fmt.Errorf("ключ %s уже зарегистрирован", org.keyID.Hex())
	}

	owner, err := ownerKey()
	if err != nil {
		return err
	}
	calldata, err := reg.ABI().Pack("registerAdminKey", org.orgID, org.qx, org.qy)
	if err != nil {
		return err
	}
	hash, err := sendOwnerTx(ctx, ec, owner, addr, calldata)
	if err != nil {
		return err
	}
	fmt.Printf("ключ админа %s добавлен в организацию %s, tx %s\n", org.keyID.Hex(), uuid.UUID(org.orgID), hash.Hex())
	return nil
}

func list(ctx context.Context) error {
	ec, addr, err := dialRegistry()
	if err != nil {
		return err
	}
	defer ec.Close()
	reg, err := chain.NewRegistry(ec, addr)
	if err != nil {
		return err
	}
	a := reg.ABI()

	logs, err := ec.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: big.NewInt(0),
		Addresses: []common.Address{addr},
		Topics: [][]common.Hash{{
			a.Events["OrganizationRegistered"].ID,
			a.Events["KeyRegistered"].ID,
			a.Events["KeyRevoked"].ID,
		}},
	})
	if err != nil {
		return fmt.Errorf("filter logs: %w", err)
	}
	orgs, err := foldLogs(a, logs)
	if err != nil {
		return err
	}
	fmt.Print(render(orgs))
	return nil
}

type orgKey struct {
	keyID  common.Hash
	role   uint8
	active bool
}

type org struct {
	id   [16]byte
	name string
	keys []orgKey
}

// foldLogs сворачивает журнал событий реестра в список организаций: порядок
// появления, у каждой — ключи в порядке регистрации. Организация без события
// OrganizationRegistered сохраняется с пустым именем.
func foldLogs(a abi.ABI, logs []types.Log) ([]org, error) {
	var orgs []org
	orgIdx := map[[16]byte]int{}
	keyLoc := map[common.Hash][2]int{}
	ensureOrg := func(id [16]byte) int {
		i, ok := orgIdx[id]
		if !ok {
			orgs = append(orgs, org{id: id})
			i = len(orgs) - 1
			orgIdx[id] = i
		}
		return i
	}

	for _, l := range logs {
		if len(l.Topics) == 0 {
			return nil, errors.New("лог без topics")
		}
		switch l.Topics[0] {
		case a.Events["OrganizationRegistered"].ID:
			if len(l.Topics) < 2 {
				return nil, errors.New("OrganizationRegistered: нет orgId в topics")
			}
			var id [16]byte
			copy(id[:], l.Topics[1][:16])
			out, err := a.Unpack("OrganizationRegistered", l.Data)
			if err != nil {
				return nil, fmt.Errorf("OrganizationRegistered: %w", err)
			}
			orgs[ensureOrg(id)].name = out[0].(string)

		case a.Events["KeyRegistered"].ID:
			if len(l.Topics) < 3 {
				return nil, errors.New("KeyRegistered: нет keyId/orgId в topics")
			}
			var id [16]byte
			copy(id[:], l.Topics[2][:16])
			out, err := a.Unpack("KeyRegistered", l.Data)
			if err != nil {
				return nil, fmt.Errorf("KeyRegistered: %w", err)
			}
			i := ensureOrg(id)
			orgs[i].keys = append(orgs[i].keys, orgKey{keyID: l.Topics[1], role: out[0].(uint8), active: true})
			keyLoc[l.Topics[1]] = [2]int{i, len(orgs[i].keys) - 1}

		case a.Events["KeyRevoked"].ID:
			if len(l.Topics) < 2 {
				return nil, errors.New("KeyRevoked: нет keyId в topics")
			}
			loc, ok := keyLoc[l.Topics[1]]
			if !ok {
				return nil, fmt.Errorf("KeyRevoked для неизвестного ключа %s", l.Topics[1].Hex())
			}
			orgs[loc[0]].keys[loc[1]].active = false
		}
	}
	return orgs, nil
}

func render(orgs []org) string {
	if len(orgs) == 0 {
		return "организаций нет\n"
	}
	var b strings.Builder
	for _, o := range orgs {
		if o.name == "" {
			fmt.Fprintf(&b, "%s\n", uuid.UUID(o.id))
		} else {
			fmt.Fprintf(&b, "%s  %s\n", uuid.UUID(o.id), o.name)
		}
		for _, k := range o.keys {
			role := "operator"
			if k.role == 1 {
				role = "admin"
			}
			state := "активен"
			if !k.active {
				state = "отозван"
			}
			fmt.Fprintf(&b, "  %s  %-10s%s\n", k.keyID.Hex(), role, state)
		}
	}
	return b.String()
}

// dialRegistry подключается к RPC и возвращает адрес реестра.
func dialRegistry() (*ethclient.Client, common.Address, error) {
	rpcURL, err := requireEnv("RPC_URL")
	if err != nil {
		return nil, common.Address{}, err
	}
	addrHex, err := requireEnv("REGISTRY_ADDR")
	if err != nil {
		return nil, common.Address{}, err
	}
	if !common.IsHexAddress(addrHex) {
		return nil, common.Address{}, fmt.Errorf("REGISTRY_ADDR: невалидный адрес %q", addrHex)
	}
	ec, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("dial RPC: %w", err)
	}
	return ec, common.HexToAddress(addrHex), nil
}

func ownerKey() (*ecdsa.PrivateKey, error) {
	v, err := requireEnv("OWNER_KEY")
	if err != nil {
		return nil, err
	}
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(v, "0x"))
	if err != nil {
		return nil, fmt.Errorf("OWNER_KEY: %w", err)
	}
	return pk, nil
}

func sendOwnerTx(ctx context.Context, ec *ethclient.Client, key *ecdsa.PrivateKey, to common.Address, data []byte) (common.Hash, error) {
	from := crypto.PubkeyToAddress(key.PublicKey)
	chainID, err := ec.ChainID(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("chain id: %w", err)
	}
	nonce, err := ec.PendingNonceAt(ctx, from)
	if err != nil {
		return common.Hash{}, fmt.Errorf("nonce: %w", err)
	}
	gasPrice, err := ec.SuggestGasPrice(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("gas price: %w", err)
	}
	gas, err := ec.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Data: data})
	if err != nil {
		return common.Hash{}, fmt.Errorf("оценка газа (OWNER_KEY не владелец реестра?): %w", err)
	}
	signed, err := types.SignTx(types.NewTransaction(nonce, to, big.NewInt(0), gas, gasPrice, data), types.NewEIP155Signer(chainID), key)
	if err != nil {
		return common.Hash{}, fmt.Errorf("sign: %w", err)
	}
	if err := ec.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, fmt.Errorf("send: %w", err)
	}
	if err := waitReceipt(ctx, ec, signed.Hash()); err != nil {
		return common.Hash{}, err
	}
	return signed.Hash(), nil
}

func waitReceipt(ctx context.Context, ec *ethclient.Client, hash common.Hash) error {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		r, err := ec.TransactionReceipt(ctx, hash)
		if err == nil {
			if r.Status != types.ReceiptStatusSuccessful {
				return fmt.Errorf("tx %s reverted", hash.Hex())
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("tx %s: нет receipt за 120 с", hash.Hex())
}

// requireEnv — значение из <NAME>_FILE или <NAME>; пустое = ошибка.
func requireEnv(name string) (string, error) {
	v := envOrFile(name)
	if v == "" {
		return "", fmt.Errorf("%s (или %s_FILE) не задан", name, name)
	}
	return v, nil
}

func envOrFile(name string) string {
	if path := os.Getenv(name + "_FILE"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return os.Getenv(name)
}
