pub fn count(n: u64) -> String {
    if n >= 1_000_000 {
        format!("{:.2}M", n as f64 / 1_000_000.)
    } else if n >= 1000 {
        format!("{:.1}K", n as f64 / 1000.)
    } else {
        n.to_string()
    }
}
pub fn parse_count(text: &str) -> Result<u64, &'static str> {
    let s = text.trim().to_ascii_uppercase();
    let (raw, factor) = if let Some(s) = s.strip_suffix('K') {
        (s, 1_000u64)
    } else if let Some(s) = s.strip_suffix('M') {
        (s, 1_000_000)
    } else {
        (s.as_str(), 1)
    };
    let parts: Vec<_> = raw.split('.').collect();
    if parts.is_empty()
        || parts.len() > 2
        || parts
            .iter()
            .any(|p| p.is_empty() || !p.bytes().all(|b| b.is_ascii_digit()))
    {
        return Err("usage-ui.enter-a-positive-integer-or-a-k");
    }
    let a = parts[0]
        .parse::<u64>()
        .ok()
        .and_then(|n| n.checked_mul(factor))
        .ok_or("usage-ui.value-is-too-large")?;
    let b = if parts.len() == 2 {
        let scale = 10u64
            .checked_pow(parts[1].len() as u32)
            .ok_or("usage-ui.too-many-decimal-places")?;
        let n = parts[1]
            .parse::<u64>()
            .ok()
            .and_then(|n| n.checked_mul(factor))
            .ok_or("usage-ui.value-is-too-large")?;
        if n % scale != 0 {
            return Err("usage-ui.token-count-must-be-a-whole-number");
        }
        n / scale
    } else {
        0
    };
    let n = a.checked_add(b).ok_or("usage-ui.value-is-too-large")?;
    if !(1..=9_007_199_254_740_991).contains(&n) {
        return Err("usage-ui.value-is-out-of-range");
    }
    Ok(n)
}
#[cfg(test)]
mod tests {
    use super::parse_count;
    #[test]
    fn limits() {
        assert_eq!(parse_count("1.5M"), Ok(1500000));
        assert_eq!(parse_count("500K"), Ok(500000));
        for s in ["0", "NaN", "1.1", "-1", "1e6", "9007199254740992"] {
            assert!(parse_count(s).is_err(), "{s}");
        }
    }
}
