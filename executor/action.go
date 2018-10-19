package executor

import (
	"time"

	"gitlab.33.cn/chain33/chain33/account"
	"gitlab.33.cn/chain33/chain33/common"
	dbm "gitlab.33.cn/chain33/chain33/common/db"
	uf "gitlab.33.cn/chain33/chain33/plugin/dapp/unfreeze/types"
	"gitlab.33.cn/chain33/chain33/system/dapp"
	"gitlab.33.cn/chain33/chain33/types"
)

type action struct {
	coinsAccount *account.DB
	db           dbm.KV
	txhash       []byte
	fromaddr     string
	blocktime    int64
	height       int64
	index        int32
	execaddr     string
}

func newAction(u *Unfreeze, tx *types.Transaction, index int32) *action {
	hash := tx.Hash()
	fromaddr := tx.From()
	return &action{u.GetCoinsAccount(), u.GetStateDB(), hash, fromaddr,
		u.GetBlockTime(), u.GetHeight(), index, dapp.ExecAddress(string(tx.Execer))}
}

//创建解冻交易
func (a *action) UnfreezeCreate(create *uf.UnfreezeCreate) (*types.Receipt, error) {
	//构造ID - txHash
	var unfreezeID string = "unfreezeID_" + common.ToHex(a.txhash)
	tokenAccDB, err := account.NewAccountDB("token", create.TokenName, a.db)
	if err != nil {
		return nil, err
	}
	receipt, err := tokenAccDB.ExecFrozen(a.fromaddr, a.execaddr, create.TotalCount)
	if err != nil {
		uflog.Error("unfreeze create ", "addr", a.fromaddr, "execaddr", a.execaddr, "ExecFrozen amount", create.TotalCount)
		return nil, err
	}
	var logs []*types.ReceiptLog
	var kv []*types.KeyValue
	logs = append(logs, receipt.Logs...)
	kv = append(kv, receipt.KV...)
	unfreeze := &uf.Unfreeze{
		UnfreezeID:  unfreezeID,
		StartTime:   create.StartTime,
		TokenName:   create.TokenName,
		TotalCount:  create.TotalCount,
		Initiator:   a.fromaddr,
		Beneficiary: create.Beneficiary,
		Period:      create.Period,
		Means:       create.Means,
		Amount:      create.Amount,
	}
	a.saveStateDB(unfreeze)
	k := []byte(unfreezeID)
	v := types.Encode(unfreeze)
	kv = append(kv, &types.KeyValue{k, v})
	receiptLog := a.getCreateLog(unfreeze)
	logs = append(logs, receiptLog)
	return &types.Receipt{types.ExecOk, kv, logs}, nil
}

//提取解冻币
func (a *action) UnfreezeWithdraw(withdraw *uf.UnfreezeWithdraw) (*types.Receipt, error) {
	value, err := a.db.Get(key(withdraw.UnfreezeID))
	if err != nil {
		uflog.Error("unfreeze withdraw ", "execaddr", a.execaddr, "err", err)
		return nil, err
	}
	var unfreeze uf.Unfreeze
	err = types.Decode(value, &unfreeze)
	if err != nil {
		uflog.Error("unfreeze withdraw ", "execaddr", a.execaddr, "err", err)
		return nil, err
	}
	var logs []*types.ReceiptLog
	var kv []*types.KeyValue
	currentTime := time.Now().Unix()
	expectTimes := (currentTime + unfreeze.Period - unfreeze.StartTime) / unfreeze.Period
	reaTimes := expectTimes - int64(unfreeze.WithdrawTimes)
	if reaTimes <= 0 {
		uflog.Error("unfreeze withdraw ", "execaddr", a.execaddr, "err", types.ErrUnfreezeBeforeDue)
		return nil, types.ErrUnfreezeBeforeDue
	}
	if unfreeze.Remaining <= 0 {
		uflog.Error("unfreeze withdraw ", "execaddr", a.execaddr, "err", types.ErrUnfreezeEmptied)
		return nil, types.ErrUnfreezeEmptied
	}

	var available int64
	switch unfreeze.Means {
	case 1: // 百分比
		for i := int64(0); i < reaTimes; i++ {
			if tmp := unfreeze.Remaining * unfreeze.Amount / 10000; tmp == 0 {
				available = unfreeze.Remaining
				break
			} else {
				available += tmp
			}
		}
	case 2: // 固额
		for i := int64(0); i < reaTimes; i++ {
			if unfreeze.Remaining <= unfreeze.Amount {
				available = unfreeze.Remaining
				break
			}
			available += unfreeze.Amount
		}
	default:
		uflog.Error("unfreeze withdraw ", "execaddr", a.execaddr, "err", types.ErrUnfreezeMeans)
		return nil, types.ErrUnfreezeMeans
	}

	tokenAccDB, err := account.NewAccountDB("token", unfreeze.TokenName, a.db)
	if err != nil {
		return nil, err
	}
	receipt, err := tokenAccDB.ExecTransferFrozen(unfreeze.Initiator, a.fromaddr, a.execaddr, available)
	if err != nil {
		uflog.Error("unfreeze withdraw ", "execaddr", a.execaddr, "err", err)
		return nil, err
	}

	logs = append(logs, receipt.Logs...)
	kv = append(kv, receipt.KV...)

	unfreeze.WithdrawTimes += int32(reaTimes)
	unfreeze.Remaining -= available
	a.saveStateDB(&unfreeze)
	receiptLog := a.getWithdrawLog(&unfreeze)
	logs = append(logs, receiptLog)
	k := []byte(withdraw.UnfreezeID)
	v := types.Encode(&unfreeze)
	kv = append(kv, &types.KeyValue{k, v})
	return &types.Receipt{types.ExecOk, kv, logs}, nil
}

//中止定期解冻
func (a *action) UnfreezeTerminate(terminate *uf.UnfreezeTerminate) (*types.Receipt, error) {
	value, err := a.db.Get(key(terminate.UnfreezeID))
	if err != nil {
		uflog.Error("unfreeze terminate ", "execaddr", a.execaddr, "err", err)
		return nil, err
	}
	var unfreeze uf.Unfreeze
	err = types.Decode(value, &unfreeze)
	if err != nil {
		uflog.Error("unfreeze terminate ", "execaddr", a.execaddr, "err", err)
		return nil, err
	}
	if a.fromaddr != unfreeze.Initiator {
		uflog.Error("unfreeze terminate ", "execaddr", a.execaddr, "err", types.ErrUnfreezeID)
		return nil, types.ErrUnfreezeID
	}
	if unfreeze.Remaining <= 0 {
		uflog.Error("unfreeze terminate ", "execaddr", a.execaddr, "err", types.ErrUnfreezeEmptied)
		return nil, types.ErrUnfreezeEmptied
	}
	tokenAccDB, err := account.NewAccountDB("token", unfreeze.TokenName, a.db)
	if err != nil {
		return nil, err
	}
	receipt, err := tokenAccDB.ExecActive(unfreeze.Initiator, a.execaddr, unfreeze.Remaining)
	if err != nil {
		uflog.Error("unfreeze terminate ", "addr", unfreeze.Initiator, "execaddr", a.execaddr, "err", err)
		return nil, err
	}
	var logs []*types.ReceiptLog
	var kv []*types.KeyValue
	logs = append(logs, receipt.Logs...)
	kv = append(kv, receipt.KV...)
	unfreeze.Remaining = 0
	a.saveStateDB(&unfreeze)
	receiptLog := a.getTerminateLog(&unfreeze)
	logs = append(logs, receiptLog)
	k := []byte(terminate.UnfreezeID)
	v := types.Encode(&unfreeze)
	kv = append(kv, &types.KeyValue{k, v})
	return &types.Receipt{types.ExecOk, kv, logs}, nil
}

func (a *action) saveStateDB(unfreeze *uf.Unfreeze) {
	a.db.Set(key(unfreeze.GetUnfreezeID()), types.Encode(unfreeze))
}

func key(id string) (keys []byte) {
	keys = append(keys, []byte("mavl-"+uf.UnfreezeX+"-")...)
	keys = append(keys, []byte(id)...)
	return keys
}

func (a *action) getCreateLog(unfreeze *uf.Unfreeze) *types.ReceiptLog {
	log := &types.ReceiptLog{}
	log.Ty = uf.TyLogCreateUnfreeze
	r := &uf.ReceiptCreate{}
	r.UnfreezeID = unfreeze.UnfreezeID
	r.Initiator = unfreeze.Initiator
	log.Log = types.Encode(r)
	return log
}

func (a *action) getWithdrawLog(unfreeze *uf.Unfreeze) *types.ReceiptLog {
	log := &types.ReceiptLog{}
	log.Ty = uf.TyLogCreateUnfreeze
	r := &uf.ReceiptWithdraw{}
	r.WithdrawTimes = unfreeze.WithdrawTimes
	r.Beneficiary = unfreeze.Beneficiary
	log.Log = types.Encode(r)
	return log
}

func (a *action) getTerminateLog(unfreeze *uf.Unfreeze) *types.ReceiptLog {
	log := &types.ReceiptLog{}
	log.Ty = uf.TyLogCreateUnfreeze
	r := &uf.ReceiptTerminate{}
	r.UnfreezeID = unfreeze.UnfreezeID
	log.Log = types.Encode(r)
	return log
}

//查询可提币状态
func QueryWithdraw(stateDB dbm.KV, param *uf.QueryWithdrawStatus) (types.Message, error) {
	//查询提币次数
	//计算当前可否提币
	return &types.Reply{}, nil
}
