use borsh::{BorshDeserialize, BorshSerialize};
use wasmlanche_sdk::{program::Program, public, state_keys, types::Address};
// new things

/// The program state keys.
#[state_keys]
enum StateKey {
    /// The total supply of the token. Key prefix 0x0.
    TotalSupply,
    /// The name of the token. Key prefix 0x1.
    Name,
    /// The symbol of the token. Key prefix 0x2.
    Symbol,
    /// The balance of the token by address. Key prefix 0x3 + address.
    Balance(Address),
}

/// Initializes the program with a name, symbol, and total supply.
#[public]
pub fn init(program: Program) -> bool {
    // set total supply
    program
        .state()
        .store(StateKey::TotalSupply, &123456789_i64)
        .expect("failed to store total supply");

    // set token name
    program
        .state()
        .store(StateKey::Name, b"WasmCoin")
        .expect("failed to store coin name");

    // set token symbol
    program
        .state()
        .store(StateKey::Symbol, b"WACK")
        .expect("failed to store symbol");

    true
}

/// Returns the total supply of the token.
#[public]
pub fn get_total_supply(program: Program) -> i64 {
    program
        .state()
        .get(StateKey::TotalSupply)
        .expect("failed to get total supply")
}

/// Transfers balance from the token owner to the recipient.
#[public]
pub fn mint_to(program: Program, recipient: Address, amount: i64) -> bool {
    let balance = program
        .state()
        .get::<i64, _>(StateKey::Balance(recipient))
        .unwrap_or_default();

    program
        .state()
        .store(StateKey::Balance(recipient), &(balance + amount))
        .expect("failed to store balance");

    true
}

/// Burn the token from the recipient.
#[public]
pub fn burn_from(program: Program, recipient: Address) -> bool {
    program
        .state()
        .delete(StateKey::Balance(recipient))
        .expect("failed to burn recipient tokens");
    true
}

/// Transfers balance from the sender to the the recipient.
#[public]
pub fn transfer(program: Program, sender: Address, recipient: Address, amount: i64) -> bool {
    assert_ne!(sender, recipient, "sender and recipient must be different");

    // ensure the sender has adequate balance
    let sender_balance = program
        .state()
        .get::<i64, _>(StateKey::Balance(sender))
        .expect("failed to update balance");

    assert!(amount >= 0 && sender_balance >= amount, "invalid input");

    let recipient_balance = program
        .state()
        .get::<i64, _>(StateKey::Balance(recipient))
        .unwrap_or_default();

    // update balances
    program
        .state()
        .store(StateKey::Balance(sender), &(sender_balance - amount))
        .expect("failed to store balance");

    program
        .state()
        .store(StateKey::Balance(recipient), &(recipient_balance + amount))
        .expect("failed to store balance");

    true
}

#[derive(BorshDeserialize, BorshSerialize)]
pub struct Minter {
    to: Address,
    amount: i32,
}

/// Mints tokens to multiple recipients.
#[public]
pub fn mint_to_many(program: Program, minters: Vec<Minter>) -> bool {
    for minter in minters.iter() {
        mint_to(program, minter.to, minter.amount as i64);
    }

    true
}

/// Gets the balance of the recipient.
#[public]
pub fn get_balance(program: Program, recipient: Address) -> i64 {
    program
        .state()
        .get(StateKey::Balance(recipient))
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use simulator::{
        id_from_step, Endpoint, Key, Operator, Param, ParamType, Plan, PlanResponse, Require,
        ResultAssertion, Step,
    };

    // TODO: find a better way to do this
    const PROGRAM_PATH: &str = "./build/wasm32-unknown-unknown/debug/token.wasm";

    #[test]
    fn test_create_program() {
        let simulator = simulator::Client::new();

        let owner_key = "owner";
        // create owner key in single step
        let resp = simulator
            .key_create::<PlanResponse>(owner_key, Key::Ed25519)
            .unwrap();
        assert_eq!(resp.error, None);

        // create a new program on chain.
        let resp = simulator.create_program(owner_key, PROGRAM_PATH).unwrap();
        assert_eq!(resp.error, None);
        assert!(resp.result.id.is_some());
    }

    #[test]
    fn test_token_plan() {
        let simulator = simulator::Client::new();

        let owner_key = "owner";
        // create owner key in single step
        let resp = simulator
            .key_create::<PlanResponse>(owner_key, Key::Ed25519)
            .unwrap();
        assert_eq!(resp.error, None);

        // create multiple step test plan
        let mut plan = Plan::new(owner_key);

        // step 0: create program
        plan.add_step(Step {
            endpoint: Endpoint::Execute,
            method: "program_create".into(),
            max_units: 0,
            params: vec![Param::new(ParamType::String, PROGRAM_PATH)],
            require: None,
        });

        // step 1: create alice key
        plan.add_step(Step {
            endpoint: Endpoint::Key,
            method: "key_create".into(),
            params: vec![Param::new(ParamType::Key(Key::Ed25519), "alice_key")],
            max_units: 0,
            require: None,
        });

        // step 2: create bob key
        plan.add_step(Step {
            endpoint: Endpoint::Key,
            method: "key_create".into(),
            params: vec![Param::new(ParamType::Key(Key::Ed25519), "bob_key")],
            max_units: 0,
            require: None,
        });

        // step 3: init token program
        plan.add_step(Step {
            endpoint: Endpoint::Execute,
            method: "init".into(),
            // program was created in step 0 so we can reference its id using the step_N identifier
            params: vec![Param::new(ParamType::Id, id_from_step(0).as_ref())],
            max_units: 1000000,
            require: None,
        });

        // step 4: mint to alice
        plan.add_step(Step {
            endpoint: Endpoint::Execute,
            method: "mint_to".into(),
            params: vec![
                Param::new(ParamType::Id, id_from_step(0).as_ref()),
                Param::new(ParamType::Key(Key::Ed25519), "alice_key"),
                Param::new(ParamType::U64, "1000"),
            ],
            max_units: 1000000,
            require: None,
        });

        // step 5: transfer 100 from alice to bob
        plan.add_step(Step {
            endpoint: Endpoint::Execute,
            method: "transfer".into(),
            params: vec![
                Param::new(ParamType::Id, id_from_step(0).as_ref()),
                Param::new(ParamType::Key(Key::Ed25519), "alice_key"),
                Param::new(ParamType::Key(Key::Ed25519), "bob_key"),
                Param::new(ParamType::U64, "100"),
            ],
            max_units: 1000000,
            require: None,
        });

        // run plan
        let plan_responses = simulator.run::<PlanResponse>(&plan).unwrap();

        // collect actual id of program from step 0
        let mut program_id = String::new();
        if let Some(step_0) = plan_responses.first() {
            program_id = step_0.result.id.clone().unwrap_or_default();
        }

        // ensure no errors
        assert!(
            plan_responses.iter().all(|resp| resp.error.is_none()),
            "error: {:?}",
            plan_responses
                .iter()
                .filter_map(|resp| resp.error.as_ref())
                .next()
        );

        // get total supply and assert result is expected
        let resp = simulator
            .read_only(
                "owner",
                "get_total_supply",
                vec![Param::new(ParamType::Id, program_id.as_ref())],
                Some(Require {
                    result: ResultAssertion {
                        operator: Operator::NumericEq,
                        value: "123456789".into(),
                    },
                }),
            )
            .expect("failed to get total supply");
        assert_eq!(resp.error, None);

        // verify alice balance is 900
        let resp = simulator
            .read_only(
                "owner",
                "get_balance",
                vec![
                    Param::new(ParamType::Id, program_id.as_ref()),
                    Param::new(ParamType::Key(Key::Ed25519), "alice_key"),
                ],
                Some(Require {
                    result: ResultAssertion {
                        operator: Operator::NumericEq,
                        value: "900".into(),
                    },
                }),
            )
            .expect("failed to get alice balance");
        assert_eq!(resp.error, None);

        // verify bob balance is 100
        let resp = simulator
            .read_only(
                "owner",
                "get_balance",
                vec![
                    Param {
                        value: program_id.clone(),
                        param_type: ParamType::Id,
                    },
                    Param {
                        value: "bob_key".into(),
                        param_type: ParamType::Key(Key::Ed25519),
                    },
                ],
                Some(Require {
                    result: ResultAssertion {
                        operator: Operator::NumericEq,
                        value: "100".into(),
                    },
                }),
            )
            .expect("failed to get bob balance");
        assert_eq!(resp.error, None);

        let resp = simulator
            .execute::<PlanResponse>(
                Step {
                    endpoint: Endpoint::Execute,
                    method: "burn_from".into(),
                    params: vec![
                        Param::new(ParamType::Id, program_id.as_ref()),
                        Param::new(ParamType::Key(Key::Ed25519), "alice_key"),
                    ],
                    max_units: 10000,
                    require: None,
                },
                owner_key,
            )
            .expect("failed to burn alice tokens");
        assert_eq!(resp.error, None);
    }
}
