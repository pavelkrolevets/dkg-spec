package spec

import (
	"crypto/rsa"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/herumi/bls-eth-go-binary/bls"
	"github.com/ssvlabs/dkg-spec/crypto"
	"github.com/ssvlabs/dkg-spec/eip1271"
)

// Init is called on operator side when a new init message is received from initiator
func (op *Operator) Init(
	init *Init,
	requestID [24]byte,
	sk *rsa.PrivateKey,
) (*Result, error) {
	if err := ValidateInitMessage(init); err != nil {
		return nil, err
	}

	var share *bls.SecretKey
	var validatorPK []byte
	/*
		DKG ceremony
		ALL participants must participate
	*/

	// sign deposit data
	depositDataRoot, err := crypto.DepositDataRootForFork(
		init.Fork,
		validatorPK,
		init.WithdrawalCredentials,
		phase0.Gwei(init.Amount),
	)
	if err != nil {
		return nil, err
	}
	depositDataSig := share.SignByte(depositDataRoot[:])

	// sign proof
	encryptedShare, err := crypto.Encrypt(&sk.PublicKey, []byte(share.SerializeToHexStr()))
	if err != nil {
		return nil, err
	}
	proof := &Proof{
		ValidatorPubKey: validatorPK,
		EncryptedShare:  encryptedShare,
		SharePubKey:     share.GetPublicKey().Serialize(),
		Owner:           init.Owner,
	}
	byts, err := proof.MarshalSSZ()
	if err != nil {
		return nil, err
	}
	proofSig, err := crypto.SignRSA(sk, byts)
	if err != nil {
		return nil, err
	}

	return &Result{
		OperatorID:                 op.ID,
		RequestID:                  requestID,
		DepositPartialSignature:    depositDataSig.Serialize(),
		OwnerNoncePartialSignature: share.SignByte(PartialNonceRoot(init.Owner, init.Nonce)).Serialize(),
		SignedProof: SignedProof{
			Proof:     proof,
			Signature: proofSig,
		},
	}, nil
}

// Reshare is called when an operator receives a reshare message
func (op *Operator) Reshare(
	signedReshare *SignedReshare,
	sk *rsa.PrivateKey,
	client eip1271.ETHClient,
) ([]*Result, error) {
	results := make([]*Result, 0)
	if len(signedReshare.Messages) == 0 {
		return nil, fmt.Errorf("no reshare messages")
	}
	reshareMsgs := make([]SSZMarshaller, 0)
	for _, msg := range signedReshare.Messages {
		reshareMsgs = append(reshareMsgs, msg)
	}
	MsgHash, err := GetBulkMessageHash(reshareMsgs)
	if err != nil {
		return nil, err
	}
	if err = crypto.VerifySignedMessageByOwner(
		client,
		signedReshare.Messages[0].Reshare.Owner,
		MsgHash,
		signedReshare.Signature,
	); err != nil {
		return nil, err
	}
	// run ceremonies one by one
	for _, reshareMsg := range signedReshare.Messages {
		position := FindOperatorPosition(reshareMsg.Reshare.OldOperators, op.ID)
		if position != -1 {
			// this operator is an old operator
			if err := ValidateReshareMessage(reshareMsg.Reshare, op, reshareMsg.Proofs[position]); err != nil {
				return nil, err
			}
		}

		var share *bls.SecretKey

		reqID, err := GetReqIDFromMsg(reshareMsg)
		if err != nil {
			return nil, err
		}

		/*
			reshare ceremony
			All new participants must participate
			T out of old participants must participate
		*/

		result, err := BuildResult(
			op.ID,
			reqID,
			share,
			sk,
			reshareMsg.Reshare.ValidatorPubKey,
			reshareMsg.Reshare.Owner,
			reshareMsg.Reshare.WithdrawalCredentials,
			reshareMsg.Reshare.Fork,
			reshareMsg.Reshare.Nonce,
			phase0.Gwei(reshareMsg.Reshare.Amount),
		)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}

	return results, nil
}

// Resign is called when an operator receives a re-sign message
func (op *Operator) Resign(
	signedResign *SignedResign,
	share *bls.SecretKey,
	sk *rsa.PrivateKey, // operator's encryption private key
	client eip1271.ETHClient,
) ([]*Result, error) {
	if len(signedResign.Messages) == 0 {
		return nil, fmt.Errorf("no reshare messages")
	}
	resignMsgs := make([]SSZMarshaller, 0)
	for _, msg := range signedResign.Messages {
		resignMsgs = append(resignMsgs, msg)
	}
	MsgHash, err := GetBulkMessageHash(resignMsgs)
	if err != nil {
		return nil, err
	}
	if err = crypto.VerifySignedMessageByOwner(
		client,
		signedResign.Messages[0].Resign.Owner,
		MsgHash,
		signedResign.Signature,
	); err != nil {
		return nil, err
	}
	results := make([]*Result, 0)
	// run ceremonies one by one
	for _, resignMsg := range signedResign.Messages {
		position := FindOperatorPosition(resignMsg.Operators, op.ID)
		if position == -1 {
			return nil, fmt.Errorf("operator not found in the list")
		}
		if err := ValidateResignMessage(resignMsg.Resign, op, resignMsg.Proofs[position]); err != nil {
			return nil, err
		}

		reqID, err := GetReqIDFromMsg(resignMsg)
		if err != nil {
			return nil, err
		}

		/*
			resign ceremony...
		*/

		result, err := BuildResult(
			op.ID,
			reqID,
			share,
			sk,
			resignMsg.Resign.ValidatorPubKey,
			resignMsg.Resign.Owner,
			resignMsg.Resign.WithdrawalCredentials,
			resignMsg.Resign.Fork,
			resignMsg.Resign.Nonce,
			phase0.Gwei(resignMsg.Resign.Amount),
		)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}
