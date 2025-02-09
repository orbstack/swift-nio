#!/usr/bin/env bash

set -eufo pipefail

IPV6_ENABLED=false
DOMAIN_PORTS=(orbstack.local:3000 debian.orb.local:8000 aaa.k8s.orb.local:80)

machine=default
self_container=orbstack-web-main
other_container=parasite
k8s_pod=k8s_controller_ingress-nginx-controller-5bdc4f464b-xbdxb_ingress-nginx_161f9570-f007-462d-9829-fbd300fd4370_0

# setup hosts:
# - machine
# - docker container
# - k8s ingress
# - k8s pod

for domain_port in ${DOMAIN_PORTS[@]}; do
    domain=${domain_port%:*}
    port=${domain_port#*:}

    # from docker machine
    echo -e "\n** $domain:80 from docker machine, ipv4"
    orb debug _docker curl -v -4 http://${domain}
    echo -e "\n** $domain:$port from docker machine, ipv4"
    orb debug _docker curl -v -4 http://${domain}:$port
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from docker machine, ipv6"
        orb debug _docker curl -v -6 http://${domain}
        echo -e "\n** $domain:$port from docker machine, ipv6"
        orb debug _docker curl -v -6 http://${domain}:$port
    fi
    echo -e "\n** $domain from docker machine, ipv4 https"
    orb debug _docker curl -v -4 https://${domain}
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from docker machine, ipv6 https"
        orb debug _docker curl -v -6 https://${domain}
    fi

    # from self container, hairpinning
    echo -e "\n** $domain:80 from container, ipv4"
    orb debug $self_container curl -v -4 http://${domain}
    echo -e "\n** $domain:$port from container, ipv4"
    orb debug $self_container curl -v -4 http://${domain}:$port
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from container, ipv6"
        orb debug $self_container curl -v -6 http://${domain}
        echo -e "\n** $domain:$port from container, ipv6"
        orb debug $self_container curl -v -6 http://${domain}:$port
    fi
    echo -e "\n** $domain from container, ipv4 https"
    orb debug $self_container curl -v -4 https://${domain}
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from container, ipv6 https"
        orb debug $self_container curl -v -6 https://${domain}
    fi

    # from other container, hairpinning
    echo -e "\n** $domain:80 from other container, ipv4"
    orb debug $other_container curl -v -4 http://${domain}
    echo -e "\n** $domain:$port from other container, ipv4"
    orb debug $other_container curl -v -4 http://${domain}:$port
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from other container, ipv6"
        orb debug $other_container curl -v -6 http://${domain}
        echo -e "\n** $domain:$port from other container, ipv6"
        orb debug $other_container curl -v -6 http://${domain}:$port
    fi
    echo -e "\n** $domain from other container, ipv4 https"
    orb debug $other_container curl -v -4 https://${domain}
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from other container, ipv6 https"
        orb debug $other_container curl -v -6 https://${domain}
    fi

    # from linux machine
    echo -e "\n** $domain:80 from linux machine, ipv4"
    orb -m $machine curl -v -4 http://${domain}
    echo -e "\n** $domain:$port from linux machine, ipv4"
    orb -m $machine curl -v -4 http://${domain}:$port
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from linux machine, ipv6"
        orb -m $machine curl -v -6 http://${domain}
        echo -e "\n** $domain:$port from linux machine, ipv6"
        orb -m $machine curl -v -6 http://${domain}:$port
    fi
    echo -e "\n** $domain from linux machine, ipv4 https"
    orb -m $machine curl -v -4 https://${domain}
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from linux machine, ipv6 https"
        orb -m $machine curl -v -6 https://${domain}
    fi

    # from pod
    echo -e "\n** $domain:80 from pod, ipv4"
    orb debug $k8s_pod curl -v -4 http://${domain}
    echo -e "\n** $domain:$port from pod, ipv4"
    orb debug $k8s_pod curl -v -4 http://${domain}:$port
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from pod, ipv6"
        orb debug $k8s_pod curl -v -6 http://${domain}
        echo -e "\n** $domain:$port from pod, ipv6"
        orb debug $k8s_pod curl -v -6 http://${domain}:$port
    fi
    echo -e "\n** $domain from pod, ipv4 https"
    orb debug $k8s_pod curl -v -4 https://${domain}
    if [ "$IPV6_ENABLED" = true ]; then
        echo -e "\n** $domain from pod, ipv6 https"
        orb debug $k8s_pod curl -v -6 https://${domain}
    fi

    # from host
    echo -e "\n** $domain:80 from host, ipv4"
    curl -v -4 http://${domain}
    echo -e "\n** $domain:$port from host, ipv4"
    curl -v -4 http://${domain}:$port
    echo -e "\n** $domain from host, ipv6"
    curl -v -6 http://${domain}
    echo -e "\n** $domain:$port from host, ipv6"
    curl -v -6 http://${domain}:$port
    echo -e "\n** $domain from host, ipv4 https"
    curl -v -4 https://${domain}
    echo -e "\n** $domain from host, ipv6 https"
    curl -v -6 https://${domain}
done
