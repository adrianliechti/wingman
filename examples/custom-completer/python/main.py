import random
import time
import grpc
import uuid

import provider_pb2
import provider_pb2_grpc

from concurrent import futures
from grpc_reflection.v1alpha import reflection

class CompleterServicer(provider_pb2_grpc.CompleterServicer):
    def Complete(self, request, context):
        text = "Please provide me more information about the topic."
        words = text.split()

        for i in range(len(words)):
            content = words[i] + " "

            time.sleep(0.3 + (0.7 * random.random()))

            yield provider_pb2.Completion(
                id=str(uuid.uuid4()),
                model="test",

                delta=provider_pb2.Message(
                    role="assistant",
                    content=[provider_pb2.Content(text=content)],
                ),
            )   

        yield provider_pb2.Completion(
            id=str(uuid.uuid4()),
            model="human",

            message=provider_pb2.Message(
                role="assistant",
                content=[provider_pb2.Content(text=text)],
            ),
        )

def serve():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))

    provider_pb2_grpc.add_CompleterServicer_to_server(CompleterServicer(), server)

    SERVICE_NAMES = (
        provider_pb2.DESCRIPTOR.services_by_name['Completer'].full_name,
        reflection.SERVICE_NAME,
    )

    reflection.enable_server_reflection(SERVICE_NAMES, server)

    server.add_insecure_port('[::]:50051')
    server.start()

    print("Completion Server started. Listening on port 50051.")
    
    server.wait_for_termination()

if __name__ == '__main__':
    serve()