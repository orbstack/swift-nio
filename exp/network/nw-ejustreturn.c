#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <unistd.h>
#include <errno.h>
#include <stdbool.h>
#include <Network/Network.h>
#include <dispatch/dispatch.h>

int main()
{
    dispatch_queue_t queue;
    {
        char name[] = "packet queue";
        queue = dispatch_queue_create(name, DISPATCH_QUEUE_SERIAL);
    }

    while (true)
    {
        nw_endpoint_t endpoint;
        {
            char hostname[] = "fc00:f00d:cafe::aaaa";
            char port[] = "53";
            endpoint = nw_endpoint_create_host(hostname, port);
        }
        nw_connection_t connection = nw_connection_create(endpoint, nw_parameters_create_secure_udp(NW_PARAMETERS_DISABLE_PROTOCOL, NW_PARAMETERS_DEFAULT_CONFIGURATION));

        nw_connection_set_queue(connection, queue);
        nw_retain(connection);
        nw_connection_start(connection);

        char buf[60];
        memset(buf, 0xaa, sizeof(buf));
        nw_connection_send(connection, dispatch_data_create(buf, sizeof(buf), queue, DISPATCH_DATA_DESTRUCTOR_DEFAULT), NW_CONNECTION_DEFAULT_MESSAGE_CONTEXT, true, ^(nw_error_t _Nullable error) {
          fprintf(stderr, "sent\n");
        });
    }

    return 0;
}
