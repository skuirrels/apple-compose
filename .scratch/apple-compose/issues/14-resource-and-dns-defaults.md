# 14 Resource and DNS defaults
Type: prototype
Status: resolved

## Question
With nothing configured, containers on Apple's runtime got 1 GB and four CPUs, and on some Macs no name ever resolved because nothing answers on the gateway resolver. What should a container that sets no limits and no nameservers get?

## Answer
Half the Mac's memory with a 2 GB floor, and every CPU. Docker imposes no limit; a VM's memory is committed only as the guest touches it, which a probe confirmed: a 12 GB container and a 1 GB one each cost about 21 MB of host memory while idle. For DNS, apple-compose probes the resolver on the gateway of the network a container joins, once per process with a 700 ms timeout, and supplies nameservers only when it is silent: the Mac's default resolvers from scutil, dropping loopback and link-local addresses a VM cannot reach, or 1.1.1.1 and 8.8.8.8 when nothing reachable is left. APPLE_COMPOSE_MEMORY, APPLE_COMPOSE_CPUS and APPLE_COMPOSE_DNS still override each, and the value `runtime` hands the setting back to the runtime. Tests construct engines with NoHostDefaults so command lines do not depend on the machine running them.
