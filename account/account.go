package account

import (
	"fmt"
	clientkeys "github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/crypto/keys"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gatechain/gatechain-load-test/internal/flags"
	"github.com/gatechain/gatechain-load-test/utils"
	"log"

	"github.com/cosmos/go-bip39"
	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/crypto"
	"strconv"
	"time"
)

var kb keys.Keybase

//func SetKeybase(_kb keys.Keybase) {
//	kb = _kb
//}

func Init() (err error) {
	kb, err = clientkeys.NewKeyBaseFromHomeFlag()
	return
}

func List() (infos []keys.Info, err error) {
	return kb.List()
}

func CreateOne() (info keys.Info, err error) {
	name := fmt.Sprintf("%s", strconv.FormatInt(time.Now().Unix(), 10))
	account := uint32(0)
	index := uint32(0)
	var bip39Passphrase string
	entropySeed, err := bip39.NewEntropy(256)
	if err != nil {
		return
	}
	mnemonic, err := bip39.NewMnemonic(entropySeed[:])
	if err != nil {
		return
	}
	info, err = kb.CreateAccount(name, mnemonic, bip39Passphrase, viper.GetString(flags.FlagPassword), account, index)
	// initial transfer coins
	command := fmt.Sprintf("gaiacli tx send validator %s 1000stake --from validator --chain-id testing --fees 1stake -y", info.GetAddress().String())
	output, err := utils.Exec_shell(command)
	fmt.Println(output)
	if err != nil {
		log.Println(err)
		return
	}
	return
}

func CreateBatch(num int) (infos []keys.Info, err error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if num <= 0 {
				return
			}
			info, err := CreateOne()
			if err == nil {
				Print(info)
				infos = append(infos, info)
			} else {
				return infos, err
			}
			num--
		}
	}
}

func Get(str string) (info keys.Info, err error) {
	addr, err := sdk.AccAddressFromBech32(str)
	if err != nil {
		return
	}
	return kb.GetByAddress(addr)
}

func FindByStr(infos []keys.Info, str string) (i keys.Info, e bool) {
	for _, info := range infos {
		addrStr := ToStr(info)
		if addrStr == str {
			return info, true
		}
	}
	return nil, false
}

func ToStr(info keys.Info) string {
	return sdk.AccAddress(info.GetPubKey().Address().Bytes()).String()
}

func Print(info keys.Info) {
	fmt.Println(info.GetName(), ToStr(info))
}

func PrintAll(infos []keys.Info) {
	fmt.Println("total account number:", len(infos))
	for _, info := range infos {
		Print(info)
	}
}

func GetPrivateKey(name string) (crypto.PrivKey, error) {
	return kb.ExportPrivateKeyObject(name, "1234567890")
}
