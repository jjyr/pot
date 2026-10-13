//! Chat assistant orchestration policy. Task execution remains behind Control ports.
pub const RESULT_PREFIX: &str = "pot-result:";
pub const MAIN_PROMPT: &str = include_str!("../prompts/main_prompt.txt");
