#!/usr/bin/env bash
# PreToolUse guard for Edit|Write: denies edits to generated Swagger output and
# to .env secret files. .env-example stays editable. Reads the hook payload on
# stdin; prints a deny decision, or nothing (allow) for any other path.
jq -r '.tool_input.file_path // empty' | {
  read -r f
  case "$f" in
    */docs/swagger/*)
      r='docs/swagger/ is generated: change the @ annotations or DTOs and run make swag instead.' ;;
    */.env | */.env.*)
      r='.env files hold secrets (HMAC key, Turnstile secret) and are not edited by Claude. Change .env-example instead, or ask the user.' ;;
    *) exit 0 ;;
  esac
  jq -cn --arg r "$r" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'
}
