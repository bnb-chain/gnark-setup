// Copyright 2020 ConsenSys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by gnark DO NOT EDIT

package plonk

import (
	"errors"
	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr/fft"
	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr/kzg"
	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr/polynomial"
	"github.com/consensys/gnark/internal/backend/bls12-377/cs"

	kzgg "github.com/consensys/gnark-crypto/kzg"
)

// ProvingKey stores the data needed to generate a proof:
// * the commitment scheme
// * ql, prepended with as many ones as they are public inputs
// * qr, qm, qo prepended with as many zeroes as there are public inputs.
// * qk, prepended with as many zeroes as public inputs, to be completed by the prover
// with the list of public inputs.
// * sigma_1, sigma_2, sigma_3 in both basis
// * the copy constraint permutation
type ProvingKey struct {
	// Verifying Key is embedded into the proving key (needed by Prove)
	Vk *VerifyingKey

	// qr,ql,qm,qo (in canonical basis).
	Ql, Qr, Qm, Qo polynomial.Polynomial

	// LQk (CQk) qk in Lagrange basis (canonical basis), prepended with as many zeroes as public inputs.
	// Storing LQk in Lagrange basis saves a fft...
	CQk, LQk polynomial.Polynomial

	// Domains used for the FFTs
	DomainNum, DomainH fft.Domain

	// s1, s2, s3 (L=Lagrange basis, C=canonical basis)
	LS1, LS2, LS3 polynomial.Polynomial
	CS1, CS2, CS3 polynomial.Polynomial

	// position -> permuted position (position in [0,3*sizeSystem-1])
	Permutation []int64
}

// VerifyingKey stores the data needed to verify a proof:
// * The commitment scheme
// * Commitments of ql prepended with as many ones as there are public inputs
// * Commitments of qr, qm, qo, qk prepended with as many zeroes as there are public inputs
// * Commitments to S1, S2, S3
type VerifyingKey struct {
	// Size circuit
	Size              uint64
	SizeInv           fr.Element
	Generator         fr.Element
	NbPublicVariables uint64

	// shifters for extending the permutation set: from s=<1,z,..,z**n-1>,
	// extended domain = s || shifter[0].s || shifter[1].s
	Shifter [2]fr.Element

	// Commitment scheme that is used for an instantiation of PLONK
	KZGSRS *kzg.SRS

	// S commitments to S1, S2, S3
	S [3]kzg.Digest

	// Commitments to ql, qr, qm, qo prepended with as many zeroes (ones for l) as there are public inputs.
	// In particular Qk is not complete.
	Ql, Qr, Qm, Qo, Qk kzg.Digest
}

// Setup sets proving and verifying keys
func Setup(spr *cs.SparseR1CS, srs *kzg.SRS) (*ProvingKey, *VerifyingKey, error) {
	var pk ProvingKey
	var vk VerifyingKey

	// The verifying key shares data with the proving key
	pk.Vk = &vk

	nbConstraints := len(spr.Constraints)
	nbAssertions := len(spr.Assertions)

	// fft domains
	sizeSystem := uint64(nbConstraints + nbAssertions + spr.NbPublicVariables) // spr.NbPublicVariables is for the placeholder constraints
	pk.DomainNum = *fft.NewDomain(sizeSystem, 0, false)
	// TODO @thomas shouldn't we use 4 * DomainNum.Cardinality here?
	pk.DomainH = *fft.NewDomain(4*sizeSystem, 1, false)

	vk.Size = pk.DomainNum.Cardinality
	vk.SizeInv.SetUint64(vk.Size).Inverse(&vk.SizeInv)
	vk.Generator.Set(&pk.DomainNum.Generator)
	vk.NbPublicVariables = uint64(spr.NbPublicVariables)

	// shifters
	vk.Shifter[0].Set(&pk.DomainNum.FinerGenerator)
	vk.Shifter[1].Square(&pk.DomainNum.FinerGenerator)

	if err := pk.InitKZG(srs); err != nil {
		return nil, nil, err
	}

	// public polynomials corresponding to constraints: [ placholders | constraints | assertions ]
	pk.Ql = make([]fr.Element, pk.DomainNum.Cardinality)
	pk.Qr = make([]fr.Element, pk.DomainNum.Cardinality)
	pk.Qm = make([]fr.Element, pk.DomainNum.Cardinality)
	pk.Qo = make([]fr.Element, pk.DomainNum.Cardinality)
	pk.CQk = make([]fr.Element, pk.DomainNum.Cardinality)
	pk.LQk = make([]fr.Element, pk.DomainNum.Cardinality)

	for i := 0; i < spr.NbPublicVariables; i++ { // placeholders (-PUB_INPUT_i + qk_i = 0) TODO should return error is size is inconsistant
		pk.Ql[i].SetOne().Neg(&pk.Ql[i])
		pk.Qr[i].SetZero()
		pk.Qm[i].SetZero()
		pk.Qo[i].SetZero()
		pk.CQk[i].SetZero()
		pk.LQk[i].SetZero() // --> to be completed by the prover
	}
	offset := spr.NbPublicVariables
	for i := 0; i < nbConstraints; i++ { // constraints

		pk.Ql[offset+i].Set(&spr.Coefficients[spr.Constraints[i].L.CoeffID()])
		pk.Qr[offset+i].Set(&spr.Coefficients[spr.Constraints[i].R.CoeffID()])
		pk.Qm[offset+i].Set(&spr.Coefficients[spr.Constraints[i].M[0].CoeffID()]).
			Mul(&pk.Qm[offset+i], &spr.Coefficients[spr.Constraints[i].M[1].CoeffID()])
		pk.Qo[offset+i].Set(&spr.Coefficients[spr.Constraints[i].O.CoeffID()])
		pk.CQk[offset+i].Set(&spr.Coefficients[spr.Constraints[i].K])
		pk.LQk[offset+i].Set(&spr.Coefficients[spr.Constraints[i].K])
	}
	offset += nbConstraints
	for i := 0; i < nbAssertions; i++ { // assertions

		pk.Ql[offset+i].Set(&spr.Coefficients[spr.Assertions[i].L.CoeffID()])
		pk.Qr[offset+i].Set(&spr.Coefficients[spr.Assertions[i].R.CoeffID()])
		pk.Qm[offset+i].Set(&spr.Coefficients[spr.Assertions[i].M[0].CoeffID()]).
			Mul(&pk.Qm[offset+i], &spr.Coefficients[spr.Assertions[i].M[1].CoeffID()])
		pk.Qo[offset+i].Set(&spr.Coefficients[spr.Assertions[i].O.CoeffID()])
		pk.CQk[offset+i].Set(&spr.Coefficients[spr.Assertions[i].K])
		pk.LQk[offset+i].Set(&spr.Coefficients[spr.Assertions[i].K])
	}

	pk.DomainNum.FFTInverse(pk.Ql, fft.DIF, 0)
	pk.DomainNum.FFTInverse(pk.Qr, fft.DIF, 0)
	pk.DomainNum.FFTInverse(pk.Qm, fft.DIF, 0)
	pk.DomainNum.FFTInverse(pk.Qo, fft.DIF, 0)
	pk.DomainNum.FFTInverse(pk.CQk, fft.DIF, 0)
	fft.BitReverse(pk.Ql)
	fft.BitReverse(pk.Qr)
	fft.BitReverse(pk.Qm)
	fft.BitReverse(pk.Qo)
	fft.BitReverse(pk.CQk)

	// build permutation. Note: at this stage, the permutation takes in account the placeholders
	buildPermutation(spr, &pk)

	// set s1, s2, s3
	computeLDE(&pk)

	// Commit to the polynomials to set up the verifying key
	var err error
	if vk.Ql, err = kzg.Commit(pk.Ql, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.Qr, err = kzg.Commit(pk.Qr, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.Qm, err = kzg.Commit(pk.Qm, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.Qo, err = kzg.Commit(pk.Qo, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.Qk, err = kzg.Commit(pk.CQk, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.S[0], err = kzg.Commit(pk.CS1, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.S[1], err = kzg.Commit(pk.CS2, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.S[2], err = kzg.Commit(pk.CS3, vk.KZGSRS); err != nil {
		return nil, nil, err
	}

	return &pk, &vk, nil

}

// buildPermutation builds the Permutation associated with a circuit.
//
// The permutation s is composed of cycles of maximum length such that
//
// 			s. (l||r||o) = (l||r||o)
//
//, where l||r||o is the concatenation of the indices of l, r, o in
// ql.l+qr.r+qm.l.r+qo.O+k = 0.
//
// The permutation is encoded as a slice s of size 3*size(l), where the
// i-th entry of l||r||o is sent to the s[i]-th entry, so it acts on a tab
// like this: for i in tab: tab[i] = tab[permutation[i]]
func buildPermutation(spr *cs.SparseR1CS, pk *ProvingKey) {

	sizeSolution := int(pk.DomainNum.Cardinality)

	// position -> variable_ID
	lro := make([]int, 3*sizeSolution)

	pk.Permutation = make([]int64, 3*sizeSolution)
	for i := 0; i < spr.NbPublicVariables; i++ { // IDs of LRO associated to placeholders (only L needs to be taken care of)

		lro[i] = i
		lro[sizeSolution+i] = 0
		lro[2*sizeSolution+i] = 0

		pk.Permutation[i] = -1
		pk.Permutation[sizeSolution+i] = -1
		pk.Permutation[2*sizeSolution+i] = -1
	}
	offset := spr.NbPublicVariables
	for i := 0; i < len(spr.Constraints); i++ { // IDs of LRO associated to constraints

		lro[offset+i] = spr.Constraints[i].L.VariableID()
		lro[sizeSolution+offset+i] = spr.Constraints[i].R.VariableID()
		lro[2*sizeSolution+offset+i] = spr.Constraints[i].O.VariableID()

		pk.Permutation[i+offset] = -1
		pk.Permutation[sizeSolution+i+offset] = -1
		pk.Permutation[2*sizeSolution+i+offset] = -1
	}
	offset += len(spr.Constraints)
	for i := 0; i < len(spr.Assertions); i++ { // IDs of LRO associated to assertions

		lro[offset+i] = spr.Assertions[i].L.VariableID()
		lro[offset+sizeSolution+i] = spr.Assertions[i].R.VariableID()
		lro[offset+2*sizeSolution+i] = spr.Assertions[i].O.VariableID()

		pk.Permutation[offset+i] = -1
		pk.Permutation[offset+sizeSolution+i] = -1
		pk.Permutation[offset+2*sizeSolution+i] = -1
	}
	offset += len(spr.Assertions)
	for i := 0; i < sizeSolution-offset; i++ {

		pk.Permutation[offset+i] = -1
		pk.Permutation[offset+sizeSolution+i] = -1
		pk.Permutation[offset+2*sizeSolution+i] = -1
	}

	nbVariables := spr.NbInternalVariables + spr.NbPublicVariables + spr.NbSecretVariables

	// map ID -> last position the ID was seen
	cycle := make([]int64, nbVariables)
	for i := 0; i < len(cycle); i++ {
		cycle[i] = -1
	}

	for i := 0; i < 3*sizeSolution; i++ {
		if cycle[lro[i]] != -1 {
			pk.Permutation[i] = cycle[lro[i]]
		}
		cycle[lro[i]] = int64(i)
	}

	// complete the Permutation by filling the first IDs encountered
	counter := nbVariables
	for iter := 0; counter > 0; iter++ {
		if pk.Permutation[iter] == -1 {
			pk.Permutation[iter] = cycle[lro[iter]]
			counter--
		}
	}

}

// computeLDE computes the LDE (Lagrange basis) of the permutations
// s1, s2, s3.
//
// ex: z gen of Z/mZ, u gen of Z/8mZ, then
//
// 1	z 	..	z**n-1	|	u	uz	..	u*z**n-1	|	u**2	u**2*z	..	u**2*z**n-1  |
//  																					 |
//        																				 | Permutation
// s11  s12 ..   s1n	   s21 s22 	 ..		s2n		     s31 	s32 	..		s3n		 v
// \---------------/       \--------------------/        \------------------------/
// 		s1 (LDE)                s2 (LDE)                          s3 (LDE)
func computeLDE(pk *ProvingKey) {

	nbElmt := int(pk.DomainNum.Cardinality)

	// sID = [1,z,..,z**n-1,u,uz,..,uz**n-1,u**2,u**2.z,..,u**2.z**n-1]
	sID := make([]fr.Element, 3*nbElmt)
	sID[0].SetOne()
	sID[nbElmt].Set(&pk.DomainNum.FinerGenerator)
	sID[2*nbElmt].Square(&pk.DomainNum.FinerGenerator)

	for i := 1; i < nbElmt; i++ {
		sID[i].Mul(&sID[i-1], &pk.DomainNum.Generator)                   // z**i -> z**i+1
		sID[i+nbElmt].Mul(&sID[nbElmt+i-1], &pk.DomainNum.Generator)     // u*z**i -> u*z**i+1
		sID[i+2*nbElmt].Mul(&sID[2*nbElmt+i-1], &pk.DomainNum.Generator) // u**2*z**i -> u**2*z**i+1
	}

	// Lagrange form of S1, S2, S3
	pk.LS1 = make(polynomial.Polynomial, nbElmt)
	pk.LS2 = make(polynomial.Polynomial, nbElmt)
	pk.LS3 = make(polynomial.Polynomial, nbElmt)
	for i := 0; i < nbElmt; i++ {
		pk.LS1[i].Set(&sID[pk.Permutation[i]])
		pk.LS2[i].Set(&sID[pk.Permutation[nbElmt+i]])
		pk.LS3[i].Set(&sID[pk.Permutation[2*nbElmt+i]])
	}

	// Canonical form of S1, S2, S3
	pk.CS1 = make(polynomial.Polynomial, nbElmt)
	pk.CS2 = make(polynomial.Polynomial, nbElmt)
	pk.CS3 = make(polynomial.Polynomial, nbElmt)
	copy(pk.CS1, pk.LS1)
	copy(pk.CS2, pk.LS2)
	copy(pk.CS3, pk.LS3)
	pk.DomainNum.FFTInverse(pk.CS1, fft.DIF, 0)
	pk.DomainNum.FFTInverse(pk.CS2, fft.DIF, 0)
	pk.DomainNum.FFTInverse(pk.CS3, fft.DIF, 0)
	fft.BitReverse(pk.CS1)
	fft.BitReverse(pk.CS2)
	fft.BitReverse(pk.CS3)

}

// InitKZG inits pk.Vk.KZG using pk.DomainNum cardinality and provided SRS
//
// This should be used after deserializing a ProvingKey
// as pk.Vk.KZG is NOT serialized
func (pk *ProvingKey) InitKZG(srs kzgg.SRS) error {
	return pk.Vk.InitKZG(srs)
}

// InitKZG inits vk.KZG using provided SRS
//
// This should be used after deserializing a VerifyingKey
// as vk.KZG is NOT serialized
//
// Note that this instantiate a new FFT domain using vk.Size
func (vk *VerifyingKey) InitKZG(srs kzgg.SRS) error {
	_srs := srs.(*kzg.SRS)

	if len(_srs.G1) < int(vk.Size) {
		return errors.New("kzg srs is too small")
	}
	vk.KZGSRS = _srs

	return nil
}

// SizePublicWitness returns the expected public witness size (number of field elements)
func (vk *VerifyingKey) SizePublicWitness() int {
	return int(vk.NbPublicVariables)
}

// VerifyingKey returns pk.Vk
func (pk *ProvingKey) VerifyingKey() interface{} {
	return pk.Vk
}
