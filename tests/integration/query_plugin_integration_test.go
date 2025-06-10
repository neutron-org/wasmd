package integration

import (
	sdkmath "cosmossdk.io/math"
	"encoding/hex"
	"fmt"
	"github.com/CosmWasm/wasmd/app"
	wasmKeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmvmtypes "github.com/CosmWasm/wasmvm/v2/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAcceptListStargateQuerier(t *testing.T) {
	wasmApp := app.SetupWithEmptyStore(t)
	ctx := wasmApp.NewUncachedContext(false, cmtproto.Header{ChainID: "foo", Height: 1, Time: time.Now()})
	err := wasmApp.StakingKeeper.SetParams(ctx, stakingtypes.DefaultParams())
	require.NoError(t, err)

	addrs := app.AddTestAddrsIncremental(wasmApp, ctx, 2, sdkmath.NewInt(1_000_000))
	accepted := wasmKeeper.AcceptedQueries{
		"/cosmos.auth.v1beta1.Query/Account": func() proto.Message { return &authtypes.QueryAccountResponse{} },
		"/no/route/to/this":                  func() proto.Message { return &authtypes.QueryAccountResponse{} },
	}

	marshal := func(pb proto.Message) []byte {
		b, err := proto.Marshal(pb)
		require.NoError(t, err)
		return b
	}

	specs := map[string]struct {
		req     *wasmvmtypes.StargateQuery
		expErr  bool
		expResp string
	}{
		"in accept list - success result": {
			req: &wasmvmtypes.StargateQuery{
				Path: "/cosmos.auth.v1beta1.Query/Account",
				Data: marshal(&authtypes.QueryAccountRequest{Address: addrs[0].String()}),
			},
			expResp: fmt.Sprintf(`{"account":{"@type":"/cosmos.auth.v1beta1.BaseAccount","address":%q,"pub_key":null,"account_number":"1","sequence":"0"}}`, addrs[0].String()),
		},
		"in accept list - error result": {
			req: &wasmvmtypes.StargateQuery{
				Path: "/cosmos.auth.v1beta1.Query/Account",
				Data: marshal(&authtypes.QueryAccountRequest{Address: sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address()).String()}),
			},
			expErr: true,
		},
		"not in accept list": {
			req: &wasmvmtypes.StargateQuery{
				Path: "/cosmos.bank.v1beta1.Query/AllBalances",
				Data: marshal(&banktypes.QueryAllBalancesRequest{Address: addrs[0].String()}),
			},
			expErr: true,
		},
		"unknown route": {
			req: &wasmvmtypes.StargateQuery{
				Path: "/no/route/to/this",
				Data: marshal(&banktypes.QueryAllBalancesRequest{Address: addrs[0].String()}),
			},
			expErr: true,
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			q := wasmKeeper.AcceptListStargateQuerier(accepted, wasmApp.GRPCQueryRouter(), wasmApp.AppCodec())
			gotBz, gotErr := q(ctx, spec.req)
			if spec.expErr {
				require.Error(t, gotErr)
				return
			}
			require.NoError(t, gotErr)
			assert.JSONEq(t, spec.expResp, string(gotBz), string(gotBz))
		})
	}
}

func TestResetProtoMarshalerAfterJsonMarshal(t *testing.T) {
	appCodec := app.MakeEncodingConfig(t).Codec

	protoMarshaler := &banktypes.QueryAllBalancesResponse{}
	expected := appCodec.MustMarshalJSON(&banktypes.QueryAllBalancesResponse{
		Balances: sdk.NewCoins(sdk.NewCoin("bar", sdkmath.NewInt(30))),
		Pagination: &query.PageResponse{
			NextKey: []byte("foo"),
		},
	})

	bz, err := hex.DecodeString("0a090a036261721202333012050a03666f6f")
	require.NoError(t, err)

	// first marshal
	response, err := wasmKeeper.ConvertProtoToJSONMarshal(appCodec, protoMarshaler, bz)
	require.NoError(t, err)
	require.Equal(t, expected, response)

	// second marshal
	response, err = wasmKeeper.ConvertProtoToJSONMarshal(appCodec, protoMarshaler, bz)
	require.NoError(t, err)
	require.Equal(t, expected, response)
}

// TestDeterministicJsonMarshal tests that we get deterministic JSON marshaled response upon
// proto struct update in the state machine.
func TestDeterministicJsonMarshal(t *testing.T) {
	testCases := []struct {
		name                string
		originalResponse    string
		updatedResponse     string
		queryPath           string
		responseProtoStruct proto.Message
		expectedProto       func() proto.Message
	}{
		/**
		   *
		   * Origin Response
		   * 0a530a202f636f736d6f732e617574682e763162657461312e426173654163636f756e74122f0a2d636f736d6f7331346c3268686a6e676c3939367772703935673867646a6871653038326375367a7732706c686b
		   *
		   * Updated Response
		   * 0a530a202f636f736d6f732e617574682e763162657461312e426173654163636f756e74122f0a2d636f736d6f7331646a783375676866736d6b6135386676673076616a6e6533766c72776b7a6a346e6377747271122d636f736d6f7331646a783375676866736d6b6135386676673076616a6e6533766c72776b7a6a346e6377747271
		  // Origin proto
		  message QueryAccountResponse {
		    // account defines the account of the corresponding address.
		    google.protobuf.Any account = 1 [(cosmos_proto.accepts_interface) = "AccountI"];
		  }
		  // Updated proto
		  message QueryAccountResponse {
		    // account defines the account of the corresponding address.
		    google.protobuf.Any account = 1 [(cosmos_proto.accepts_interface) = "AccountI"];
		    // address is the address to query for.
		  	string address = 2;
		  }
		*/
		{
			"Query Account",
			"0a530a202f636f736d6f732e617574682e763162657461312e426173654163636f756e74122f0a2d636f736d6f733166387578756c746e3873717a687a6e72737a3371373778776171756867727367366a79766679",
			"0a530a202f636f736d6f732e617574682e763162657461312e426173654163636f756e74122f0a2d636f736d6f733166387578756c746e3873717a687a6e72737a3371373778776171756867727367366a79766679122d636f736d6f733166387578756c746e3873717a687a6e72737a3371373778776171756867727367366a79766679",
			"/cosmos.auth.v1beta1.Query/Account",
			&authtypes.QueryAccountResponse{},
			func() proto.Message {
				account := authtypes.BaseAccount{
					Address: "cosmos1f8uxultn8sqzhznrsz3q77xwaquhgrsg6jyvfy",
				}
				accountResponse, err := codectypes.NewAnyWithValue(&account)
				require.NoError(t, err)
				return &authtypes.QueryAccountResponse{
					Account: accountResponse,
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Case %s", tc.name), func(t *testing.T) {
			appCodec := app.MakeEncodingConfig(t).Codec

			originVersionBz, err := hex.DecodeString(tc.originalResponse)
			require.NoError(t, err)
			jsonMarshalledOriginalBz, err := wasmKeeper.ConvertProtoToJSONMarshal(appCodec, tc.responseProtoStruct, originVersionBz)
			require.NoError(t, err)

			newVersionBz, err := hex.DecodeString(tc.updatedResponse)
			require.NoError(t, err)
			jsonMarshalledUpdatedBz, err := wasmKeeper.ConvertProtoToJSONMarshal(appCodec, tc.responseProtoStruct, newVersionBz)
			require.NoError(t, err)

			// json marshaled bytes should be the same since we use the same proto struct for unmarshalling
			require.Equal(t, jsonMarshalledOriginalBz, jsonMarshalledUpdatedBz)

			// raw build also make same result
			jsonMarshalExpectedResponse, err := appCodec.MarshalJSON(tc.expectedProto())
			require.NoError(t, err)
			require.Equal(t, jsonMarshalledUpdatedBz, jsonMarshalExpectedResponse)
		})
	}
}

func TestConvertProtoToJSONMarshal(t *testing.T) {
	testCases := []struct {
		name                  string
		queryPath             string
		protoResponseStruct   proto.Message
		originalResponse      string
		expectedProtoResponse proto.Message
		expectedError         bool
	}{
		{
			name:                "successful conversion from proto response to json marshaled response",
			queryPath:           "/cosmos.bank.v1beta1.Query/AllBalances",
			originalResponse:    "0a090a036261721202333012050a03666f6f",
			protoResponseStruct: &banktypes.QueryAllBalancesResponse{},
			expectedProtoResponse: &banktypes.QueryAllBalancesResponse{
				Balances: sdk.NewCoins(sdk.NewCoin("bar", sdkmath.NewInt(30))),
				Pagination: &query.PageResponse{
					NextKey: []byte("foo"),
				},
			},
		},
		{
			name:                "invalid proto response struct",
			queryPath:           "/cosmos.bank.v1beta1.Query/AllBalances",
			originalResponse:    "0a090a036261721202333012050a03666f6f",
			protoResponseStruct: &authtypes.QueryAccountResponse{},
			expectedError:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Case %s", tc.name), func(t *testing.T) {
			originalVersionBz, err := hex.DecodeString(tc.originalResponse)
			require.NoError(t, err)
			appCodec := app.MakeEncodingConfig(t).Codec

			jsonMarshalledResponse, err := wasmKeeper.ConvertProtoToJSONMarshal(appCodec, tc.protoResponseStruct, originalVersionBz)
			if tc.expectedError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// check response by json marshaling proto response into json response manually
			jsonMarshalExpectedResponse, err := appCodec.MarshalJSON(tc.expectedProtoResponse)
			require.NoError(t, err)
			require.JSONEq(t, string(jsonMarshalledResponse), string(jsonMarshalExpectedResponse))
		})
	}
}
