#!/bin/bash
set -euo pipefail  # Exit on error, undefined vars, and pipeline failures
IFS=$'\n\t'       # Stricter word splitting

clear_rules() {
  iptables -P INPUT ACCEPT
  iptables -P OUTPUT ACCEPT
  iptables -P FORWARD ACCEPT
  iptables -F
  iptables -X
  iptables -t nat -F
  iptables -t nat -X
  iptables -t mangle -F
  iptables -t mangle -X
  ipset destroy allowed-domains 2>/dev/null || true
}

resolve_ipv4() {
  local domain="$1"
  local visited="${2:-}"

  if [[ " $visited " == *" $domain "* ]]; then
    echo "ERROR: Detected DNS loop while resolving $domain" >&2
    return 1
  fi

  local records
  if ! records=$(dig +short A "$domain"); then
    echo "ERROR: Failed to resolve $domain" >&2
    return 1
  fi

  if [ -z "$records" ]; then
    echo "ERROR: Failed to resolve $domain" >&2
    return 1
  fi

  local new_visited="$visited $domain"
  local found=false

  while read -r record; do
    [[ -z "$record" ]] && continue
    if [[ "$record" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
      echo "$record"
      found=true
    else
      echo "Following CNAME $record for $domain" >&2
      if ! resolve_ipv4 "$record" "$new_visited"; then
        return 1
      fi
      found=true
    fi
  done <<< "$records"

  if [ "$found" = false ]; then
    echo "ERROR: No IPv4 addresses found for $domain" >&2
    return 1
  fi
}

if [[ "${1:-}" == "--clear" ]]; then
  clear_rules
  echo "Firewall rules cleared"
  exit 0
fi

# Flush existing rules and delete existing ipsets
clear_rules

# First allow DNS and localhost before any restrictions
# Allow outbound DNS
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
# Allow inbound DNS responses
iptables -A INPUT -p udp --sport 53 -j ACCEPT
# Allow localhost
iptables -A INPUT -i lo -j ACCEPT
iptables -A OUTPUT -o lo -j ACCEPT

# Create ipset with CIDR support
ipset create allowed-domains hash:net

# Resolve and add other allowed domains
allowed_domains=(
    "api.anthropic.com"
    "api.business.githubcopilot.com"
    "api.enterprise.githubcopilot.com"
    "api.github.com"
    "api.githubcopilot.com"
    "api.individual.githubcopilot.com"
    "api.openai.com"
    "collector.github.com"
    "copilot-proxy.githubusercontent.com"
    "copilot-telemetry.githubusercontent.com"
    "default.exp-tas.com"
    "docs.google.com"
    "docs.googleapis.com"
    "generativelanguage.googleapis.com"
    "github.com"
    "githubcopilot.com"
    "host.docker.internal"
    "oauth2.googleapis.com"
    "origin-tracker.githubusercontent.com"
    "registry.npmjs.org"
    "sentry.io"
    "statsig.anthropic.com"
    "statsig.com"
    "www.googleapis.com"
    "www.nickhedberg.com"
)

if [[ -n "${EXTRA_ALLOWED_DOMAINS:-}" ]]; then
    for extra_domain in ${EXTRA_ALLOWED_DOMAINS}; do
        allowed_domains+=("$extra_domain")
    done
fi

for domain in "${allowed_domains[@]}"; do
    echo "Resolving $domain..."
    mapfile -t domain_ips < <(resolve_ipv4 "$domain")
    if [ "${#domain_ips[@]}" -eq 0 ]; then
        echo "ERROR: No IPv4 addresses found for $domain"
        exit 1
    fi

    for ip in "${domain_ips[@]}"; do
        echo "Adding $ip for $domain"
        ipset add allowed-domains "$ip" -exist
    done
done

# Get host IP from default route
HOST_IP=$(ip route | grep default | cut -d" " -f3)
if [ -z "$HOST_IP" ]; then
    echo "ERROR: Failed to detect host IP"
    exit 1
fi

HOST_NETWORK=$(echo "$HOST_IP" | sed "s/\.[0-9]*$/.0\/24/")
echo "Host network detected as: $HOST_NETWORK"

# Detect all Docker bridge networks by looking at the routing table
echo "Detecting all Docker bridge networks..."
DOCKER_NETWORKS=()
while IFS= read -r route; do
    # Extract network CIDR from routes (e.g., "172.18.0.0/16")
    if [[ "$route" =~ ^([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+) ]]; then
        network="${BASH_REMATCH[1]}"
        # Only include private IP ranges used by Docker (172.17-32.x.x, 192.168.x.x, 10.x.x.x)
        if [[ "$network" =~ ^172\.(1[7-9]|2[0-9]|3[0-2])\. ]] || \
           [[ "$network" =~ ^192\.168\. ]] || \
           [[ "$network" =~ ^10\. ]]; then
            DOCKER_NETWORKS+=("$network")
            echo "  Found Docker network: $network"
        fi
    fi
done < <(ip route show | grep -v default)

# If no networks detected, fall back to just the host network
if [ "${#DOCKER_NETWORKS[@]}" -eq 0 ]; then
    DOCKER_NETWORKS=("$HOST_NETWORK")
    echo "  No additional networks found, using only: $HOST_NETWORK"
fi

# Set up remaining iptables rules - allow all Docker bridge networks
for network in "${DOCKER_NETWORKS[@]}"; do
    echo "Allowing traffic to/from: $network"
    iptables -A INPUT -s "$network" -j ACCEPT
    iptables -A OUTPUT -d "$network" -j ACCEPT
done

# Set default policies to DROP first
iptables -P INPUT DROP
iptables -P FORWARD DROP
iptables -P OUTPUT DROP

# First allow established connections for already approved traffic
iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# Then allow only specific outbound traffic to allowed domains
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

# Append final REJECT rules for immediate error responses
# For TCP traffic, send a TCP reset; for UDP, send ICMP port unreachable.
iptables -A INPUT -p tcp -j REJECT --reject-with tcp-reset
iptables -A INPUT -p udp -j REJECT --reject-with icmp-port-unreachable
iptables -A OUTPUT -p tcp -j REJECT --reject-with tcp-reset
iptables -A OUTPUT -p udp -j REJECT --reject-with icmp-port-unreachable
iptables -A FORWARD -p tcp -j REJECT --reject-with tcp-reset
iptables -A FORWARD -p udp -j REJECT --reject-with icmp-port-unreachable

echo "Firewall configuration complete"
echo "Verifying firewall rules..."
if curl --connect-timeout 5 https://example.com >/dev/null 2>&1; then
    echo "ERROR: Firewall verification failed - was able to reach https://example.com"
    exit 1
else
    echo "Firewall verification passed - unable to reach https://example.com as expected"
fi

# Verify OpenAI API access
if ! curl --connect-timeout 5 https://api.openai.com >/dev/null 2>&1; then
    echo "ERROR: Firewall verification failed - unable to reach https://api.openai.com"
    exit 1
else
    echo "Firewall verification passed - able to reach https://api.openai.com as expected"
fi

# Verify Anthropic API access
if ! curl --connect-timeout 5 https://api.anthropic.com >/dev/null 2>&1; then
    echo "ERROR: Firewall verification failed - unable to reach https://api.anthropic.com"
    exit 1
else
    echo "Firewall verification passed - able to reach https://api.anthropic.com as expected"
fi
