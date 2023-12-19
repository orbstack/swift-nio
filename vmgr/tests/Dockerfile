FROM alpine AS stage1
COPY docker.sh /
RUN echo hi

FROM alpine AS stage2
COPY --from=stage1 /docker.sh /
RUN echo hi
